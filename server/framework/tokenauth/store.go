package tokenauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RefreshTokenTableName    = "aginex_api_refresh_tokens"
	refreshUserLockTableName = "aginex_api_refresh_user_locks"
)

// TokenHash is the fixed-size keyed digest passed to refresh stores. It is not
// a raw bearer credential.
type TokenHash [sha256.Size]byte

func (hash TokenHash) databaseValue() string {
	return hex.EncodeToString(hash[:])
}

func (hash TokenHash) valid() bool {
	var empty TokenHash
	return !hmac.Equal(hash[:], empty[:])
}

// NewRefreshToken is the initial active token stored for one device family.
type NewRefreshToken struct {
	ID        string
	FamilyID  string
	UserID    string
	Hash      TokenHash
	Device    Device
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// RefreshReplacement is the secret-bearing part of a rotation. The store
// copies user, family, and device metadata from the consumed token.
type RefreshReplacement struct {
	ID        string
	Hash      TokenHash
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// RefreshSession is safe session metadata; it never contains the raw token or
// its keyed hash.
type RefreshSession struct {
	ID           string
	FamilyID     string
	ParentID     string
	UserID       string
	Device       Device
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	UsedAt       *time.Time
	RevokedAt    *time.Time
	RevokeReason string
}

// RefreshStore is the persistence boundary for opaque, rotating tokens.
type RefreshStore interface {
	Create(context.Context, NewRefreshToken) (RefreshSession, error)
	Lookup(context.Context, TokenHash) (RefreshSession, error)
	Rotate(context.Context, TokenHash, RefreshReplacement, time.Time) (RefreshSession, error)
	RevokeFamily(context.Context, string, string, time.Time, string) (int64, error)
	RevokeDevice(context.Context, string, string, time.Time, string) (int64, error)
	RevokeUser(context.Context, string, time.Time, string) (int64, error)
}

// GORMStore implements refresh rotation with conditional writes portable
// across SQLite, PostgreSQL, and MySQL. It never creates schema implicitly.
type GORMStore struct {
	db *gorm.DB
}

var _ RefreshStore = (*GORMStore)(nil)

// NewGORMStore constructs the optional persistence provider.
func NewGORMStore(db *gorm.DB) (*GORMStore, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	return &GORMStore{db: db}, nil
}

func (store *GORMStore) Create(
	ctx context.Context,
	token NewRefreshToken,
) (RefreshSession, error) {
	if ctx == nil {
		return RefreshSession{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if store == nil || store.db == nil {
		return RefreshSession{}, ErrDatabaseRequired
	}
	if err := validateNewRefreshToken(token); err != nil {
		return RefreshSession{}, err
	}
	row, err := newRefreshRow(token)
	if err != nil {
		return RefreshSession{}, err
	}
	var created RefreshSession
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRefreshUser(
			tx,
			token.UserID,
			token.IssuedAt,
		); err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("store initial refresh token: %w", err)
		}
		var sessionErr error
		created, sessionErr = row.session()
		return sessionErr
	})
	if err != nil {
		return RefreshSession{}, fmt.Errorf("store initial refresh token: %w", err)
	}
	return created, nil
}

func (store *GORMStore) Lookup(ctx context.Context, hash TokenHash) (RefreshSession, error) {
	if ctx == nil {
		return RefreshSession{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if store == nil || store.db == nil {
		return RefreshSession{}, ErrDatabaseRequired
	}
	if !hash.valid() {
		return RefreshSession{}, ErrRefreshInvalid
	}
	var row refreshTokenRow
	err := store.db.WithContext(ctx).
		Where("token_hash = ?", hash.databaseValue()).
		Take(&row).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RefreshSession{}, ErrRefreshInvalid
	}
	if err != nil {
		return RefreshSession{}, fmt.Errorf("lookup refresh token: %w", err)
	}
	return row.session()
}

func (store *GORMStore) Rotate(
	ctx context.Context,
	currentHash TokenHash,
	replacement RefreshReplacement,
	now time.Time,
) (RefreshSession, error) {
	if ctx == nil {
		return RefreshSession{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if store == nil || store.db == nil {
		return RefreshSession{}, ErrDatabaseRequired
	}
	if !currentHash.valid() {
		return RefreshSession{}, ErrRefreshInvalid
	}
	if now.IsZero() || now.Unix() <= 0 {
		return RefreshSession{}, fmt.Errorf("%w: rotation time is invalid", ErrInvalidRequest)
	}
	now = utc(now)
	var (
		rotated RefreshSession
		outcome error
	)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity refreshTokenRow
		queryErr := tx.Select("user_id").
			Where("token_hash = ?", currentHash.databaseValue()).
			Take(&identity).
			Error
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			outcome = ErrRefreshInvalid
			return nil
		}
		if queryErr != nil {
			return fmt.Errorf("locate refresh token subject: %w", queryErr)
		}
		if err := lockRefreshUser(tx, identity.UserID, now); err != nil {
			return err
		}
		var current refreshTokenRow
		queryErr = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", currentHash.databaseValue()).
			Take(&current).
			Error
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			outcome = ErrRefreshInvalid
			return nil
		}
		if queryErr != nil {
			return fmt.Errorf("read refresh token under subject lock: %w", queryErr)
		}

		switch current.Status {
		case RefreshStatusActive:
			if !current.ExpiresAt.After(now) {
				result := tx.Model(&refreshTokenRow{}).
					Where(
						"id = ? AND status = ?",
						current.ID,
						RefreshStatusActive,
					).
					Updates(map[string]any{
						"status":        RefreshStatusRevoked,
						"revoked_at":    now,
						"revoke_reason": "expired",
					})
				if result.Error != nil {
					return fmt.Errorf(
						"expire refresh token: %w",
						result.Error,
					)
				}
				outcome = ErrRefreshExpired
				return nil
			}
			if err := validateRefreshReplacement(replacement, now); err != nil {
				return err
			}
			claimed := tx.Model(&refreshTokenRow{}).
				Where(
					"id = ? AND status = ? AND expires_at > ?",
					current.ID,
					RefreshStatusActive,
					now,
				).
				Updates(map[string]any{
					"status":  RefreshStatusUsed,
					"used_at": now,
				})
			if claimed.Error != nil {
				return fmt.Errorf(
					"claim refresh token: %w",
					claimed.Error,
				)
			}
			if claimed.RowsAffected != 1 {
				return fmt.Errorf(
					"%w: active refresh token could not be claimed",
					ErrInvalidState,
				)
			}
			child := refreshTokenRow{
				ID:             replacement.ID,
				TokenHash:      replacement.Hash.databaseValue(),
				FamilyID:       current.FamilyID,
				ParentID:       current.ID,
				UserID:         current.UserID,
				DeviceID:       current.DeviceID,
				DeviceName:     current.DeviceName,
				DevicePlatform: current.DevicePlatform,
				DeviceMetadata: current.DeviceMetadata,
				Status:         RefreshStatusActive,
				IssuedAt:       utc(replacement.IssuedAt),
				ExpiresAt:      utc(replacement.ExpiresAt),
				CreatedAt:      utc(replacement.IssuedAt),
			}
			if err := tx.Create(&child).Error; err != nil {
				return fmt.Errorf("store rotated refresh token: %w", err)
			}
			var sessionErr error
			rotated, sessionErr = child.session()
			return sessionErr
		case RefreshStatusUsed:
			if err := revokeActiveFamily(tx, current.UserID, current.FamilyID, now, "refresh_replay"); err != nil {
				return err
			}
			outcome = ErrRefreshReplay
		case RefreshStatusRevoked:
			outcome = ErrRefreshRevoked
		default:
			return fmt.Errorf("%w: unknown refresh status %q", ErrInvalidState, current.Status)
		}
		return nil
	})
	if err != nil {
		return RefreshSession{}, err
	}
	if outcome != nil {
		return RefreshSession{}, outcome
	}
	return rotated, nil
}

func (store *GORMStore) RevokeFamily(
	ctx context.Context,
	userID string,
	familyID string,
	now time.Time,
	reason string,
) (int64, error) {
	if err := store.validateRevocation(ctx, userID, now, reason); err != nil {
		return 0, err
	}
	if !validID(familyID) {
		return 0, fmt.Errorf("%w: family ID is required", ErrInvalidRequest)
	}
	return store.revoke(
		ctx,
		userID,
		now,
		func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&refreshTokenRow{}).
				Where(
					"user_id = ? AND family_id = ? AND status = ?",
					userID,
					familyID,
					RefreshStatusActive,
				).
				Updates(revocationUpdates(now, reason))
		},
		"revoke refresh family",
	)
}

func (store *GORMStore) RevokeDevice(
	ctx context.Context,
	userID string,
	deviceID string,
	now time.Time,
	reason string,
) (int64, error) {
	if err := store.validateRevocation(ctx, userID, now, reason); err != nil {
		return 0, err
	}
	if !validID(deviceID) {
		return 0, fmt.Errorf("%w: device ID is required", ErrInvalidRequest)
	}
	return store.revoke(
		ctx,
		userID,
		now,
		func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&refreshTokenRow{}).
				Where(
					"user_id = ? AND device_id = ? AND status = ?",
					userID,
					deviceID,
					RefreshStatusActive,
				).
				Updates(revocationUpdates(now, reason))
		},
		"revoke device refresh tokens",
	)
}

func (store *GORMStore) RevokeUser(
	ctx context.Context,
	userID string,
	now time.Time,
	reason string,
) (int64, error) {
	if err := store.validateRevocation(ctx, userID, now, reason); err != nil {
		return 0, err
	}
	return store.revoke(
		ctx,
		userID,
		now,
		func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&refreshTokenRow{}).
				Where(
					"user_id = ? AND status = ?",
					userID,
					RefreshStatusActive,
				).
				Updates(revocationUpdates(now, reason))
		},
		"revoke user refresh tokens",
	)
}

func (store *GORMStore) revoke(
	ctx context.Context,
	userID string,
	now time.Time,
	mutation func(*gorm.DB) *gorm.DB,
	operation string,
) (int64, error) {
	var affected int64
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRefreshUser(tx, userID, now); err != nil {
			return err
		}
		result := mutation(tx)
		if result == nil {
			return fmt.Errorf("%w: revocation mutation is required", ErrInvalidState)
		}
		if result.Error != nil {
			return fmt.Errorf("%s: %w", operation, result.Error)
		}
		affected = result.RowsAffected
		return nil
	})
	return affected, err
}

func (store *GORMStore) validateRevocation(
	ctx context.Context,
	userID string,
	now time.Time,
	reason string,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if store == nil || store.db == nil {
		return ErrDatabaseRequired
	}
	if !validID(userID) {
		return fmt.Errorf("%w: user ID is required", ErrInvalidRequest)
	}
	if now.IsZero() || now.Unix() <= 0 {
		return fmt.Errorf("%w: revocation time is required", ErrInvalidRequest)
	}
	if !validReason(reason) {
		return fmt.Errorf("%w: revocation reason is invalid", ErrInvalidRequest)
	}
	return nil
}

type refreshTokenRow struct {
	ID             string     `gorm:"column:id;primaryKey;size:64"`
	TokenHash      string     `gorm:"column:token_hash;size:64;not null;uniqueIndex"`
	FamilyID       string     `gorm:"column:family_id;size:64;not null;index"`
	ParentID       string     `gorm:"column:parent_id;size:64"`
	UserID         string     `gorm:"column:user_id;size:191;not null;index:idx_aginex_refresh_user_device,priority:1"`
	DeviceID       string     `gorm:"column:device_id;size:191;not null;index:idx_aginex_refresh_user_device,priority:2"`
	DeviceName     string     `gorm:"column:device_name;size:256"`
	DevicePlatform string     `gorm:"column:device_platform;size:64"`
	DeviceMetadata string     `gorm:"column:device_metadata;type:text;not null"`
	Status         string     `gorm:"column:status;size:16;not null;index"`
	IssuedAt       time.Time  `gorm:"column:issued_at;not null"`
	ExpiresAt      time.Time  `gorm:"column:expires_at;not null;index"`
	UsedAt         *time.Time `gorm:"column:used_at"`
	RevokedAt      *time.Time `gorm:"column:revoked_at"`
	RevokeReason   string     `gorm:"column:revoke_reason;size:64"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
}

type refreshUserLockRow struct {
	UserID    string    `gorm:"column:user_id;primaryKey;size:191"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (refreshUserLockRow) TableName() string {
	return refreshUserLockTableName
}

func lockRefreshUser(
	tx *gorm.DB,
	userID string,
	now time.Time,
) error {
	if tx == nil || !validID(userID) || now.IsZero() {
		return fmt.Errorf(
			"%w: refresh user lock request is invalid",
			ErrInvalidState,
		)
	}
	row := refreshUserLockRow{
		UserID:    userID,
		UpdatedAt: utc(now),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).
		Error; err != nil {
		return fmt.Errorf("create refresh user lock: %w", err)
	}
	result := tx.Model(&refreshUserLockRow{}).
		Where("user_id = ?", userID).
		UpdateColumn("updated_at", utc(now))
	if result.Error != nil {
		return fmt.Errorf("lock refresh user: %w", result.Error)
	}
	return nil
}

func (refreshTokenRow) TableName() string {
	return RefreshTokenTableName
}

func newRefreshRow(token NewRefreshToken) (refreshTokenRow, error) {
	metadata, err := json.Marshal(token.Device.Metadata)
	if err != nil {
		return refreshTokenRow{}, fmt.Errorf("%w: encode device metadata: %v", ErrInvalidRequest, err)
	}
	if len(metadata) > maxMetadataJSON {
		return refreshTokenRow{}, fmt.Errorf("%w: encoded device metadata is too large", ErrInvalidRequest)
	}
	return refreshTokenRow{
		ID:             token.ID,
		TokenHash:      token.Hash.databaseValue(),
		FamilyID:       token.FamilyID,
		UserID:         token.UserID,
		DeviceID:       token.Device.ID,
		DeviceName:     token.Device.Name,
		DevicePlatform: token.Device.Platform,
		DeviceMetadata: string(metadata),
		Status:         RefreshStatusActive,
		IssuedAt:       utc(token.IssuedAt),
		ExpiresAt:      utc(token.ExpiresAt),
		CreatedAt:      utc(token.IssuedAt),
	}, nil
}

func (row refreshTokenRow) session() (RefreshSession, error) {
	_, hashErr := hex.DecodeString(row.TokenHash)
	if !validID(row.ID) ||
		!validID(row.FamilyID) ||
		!validID(row.UserID) ||
		!validID(row.DeviceID) ||
		(row.ParentID != "" && !validID(row.ParentID)) ||
		len(row.TokenHash) != sha256.Size*2 ||
		hashErr != nil ||
		row.IssuedAt.IsZero() ||
		!row.ExpiresAt.After(row.IssuedAt) {
		return RefreshSession{}, fmt.Errorf("%w: corrupt refresh token row %q", ErrInvalidState, row.ID)
	}
	switch row.Status {
	case RefreshStatusActive:
		if row.UsedAt != nil || row.RevokedAt != nil || row.RevokeReason != "" {
			return RefreshSession{}, fmt.Errorf("%w: active refresh token has terminal metadata", ErrInvalidState)
		}
	case RefreshStatusUsed:
		if row.UsedAt == nil {
			return RefreshSession{}, fmt.Errorf("%w: used refresh token has no use time", ErrInvalidState)
		}
	case RefreshStatusRevoked:
		if row.RevokedAt == nil || !validReason(row.RevokeReason) {
			return RefreshSession{}, fmt.Errorf("%w: revoked refresh token has invalid metadata", ErrInvalidState)
		}
	default:
		return RefreshSession{}, fmt.Errorf("%w: corrupt refresh status %q", ErrInvalidState, row.Status)
	}
	if len(row.DeviceMetadata) > maxMetadataJSON {
		return RefreshSession{}, fmt.Errorf("%w: persisted device metadata is too large", ErrInvalidState)
	}
	metadata := map[string]string{}
	if err := json.Unmarshal([]byte(row.DeviceMetadata), &metadata); err != nil {
		return RefreshSession{}, fmt.Errorf("%w: decode device metadata: %v", ErrInvalidState, err)
	}
	device := Device{
		ID:       row.DeviceID,
		Name:     row.DeviceName,
		Platform: row.DevicePlatform,
		Metadata: metadata,
	}
	if err := validateDevice(device); err != nil {
		return RefreshSession{}, fmt.Errorf("%w: persisted device metadata: %v", ErrInvalidState, err)
	}
	return RefreshSession{
		ID:           row.ID,
		FamilyID:     row.FamilyID,
		ParentID:     row.ParentID,
		UserID:       row.UserID,
		Device:       device,
		Status:       row.Status,
		IssuedAt:     utc(row.IssuedAt),
		ExpiresAt:    utc(row.ExpiresAt),
		UsedAt:       cloneTime(row.UsedAt),
		RevokedAt:    cloneTime(row.RevokedAt),
		RevokeReason: row.RevokeReason,
	}, nil
}

func validateNewRefreshToken(token NewRefreshToken) error {
	if !validID(token.ID) || !validID(token.FamilyID) || !validID(token.UserID) {
		return fmt.Errorf("%w: refresh token, family, and user IDs are required", ErrInvalidRequest)
	}
	if !token.Hash.valid() {
		return fmt.Errorf("%w: refresh token hash is required", ErrInvalidRequest)
	}
	if err := validateDevice(token.Device); err != nil {
		return err
	}
	if token.IssuedAt.IsZero() ||
		token.IssuedAt.Unix() <= 0 ||
		!token.ExpiresAt.After(token.IssuedAt) {
		return fmt.Errorf("%w: refresh token timestamps are invalid", ErrInvalidRequest)
	}
	return nil
}

func validateRefreshReplacement(replacement RefreshReplacement, now time.Time) error {
	if !validID(replacement.ID) || !replacement.Hash.valid() {
		return fmt.Errorf("%w: replacement ID and hash are required", ErrInvalidRequest)
	}
	if replacement.IssuedAt.IsZero() ||
		replacement.IssuedAt.Unix() <= 0 ||
		!utc(replacement.IssuedAt).Equal(now) ||
		!replacement.ExpiresAt.After(replacement.IssuedAt) {
		return fmt.Errorf("%w: replacement timestamps are invalid", ErrInvalidRequest)
	}
	return nil
}

func revokeActiveFamily(
	tx *gorm.DB,
	userID string,
	familyID string,
	now time.Time,
	reason string,
) error {
	result := tx.Model(&refreshTokenRow{}).
		Where("user_id = ? AND family_id = ? AND status = ?", userID, familyID, RefreshStatusActive).
		Updates(revocationUpdates(now, reason))
	if result.Error != nil {
		return fmt.Errorf("revoke replayed refresh family: %w", result.Error)
	}
	return nil
}

func revocationUpdates(now time.Time, reason string) map[string]any {
	return map[string]any{
		"status":        RefreshStatusRevoked,
		"revoked_at":    utc(now),
		"revoke_reason": reason,
	}
}

func validReason(reason string) bool {
	if reason == "" || len(reason) > 64 {
		return false
	}
	for _, character := range reason {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '_', character == '-', character == '.':
		default:
			return false
		}
	}
	return true
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := utc(*value)
	return &cloned
}
