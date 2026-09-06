package tokenauth

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

const (
	// UserTableName and UserIdentityTableName are the stable table contracts
	// created by Aginex's core Goose migrations.
	UserTableName         = "users"
	UserIdentityTableName = "user_identities"

	defaultActiveStatus = "active"
)

// GORMIdentityLookup implements both provider/subject mapping and active
// SubjectLookup for Aginex's standard users and user_identities tables. It
// performs read-only queries and never creates or mutates schema.
type GORMIdentityLookup struct {
	db *gorm.DB
}

var (
	_ IdentityLookup = (*GORMIdentityLookup)(nil)
	_ SubjectLookup  = (*GORMIdentityLookup)(nil)
)

// NewGORMIdentityLookup constructs a lookup for Aginex's standard identity
// schema. Applications with a different schema implement IdentityLookup and
// SubjectLookup directly.
func NewGORMIdentityLookup(
	db *gorm.DB,
) (*GORMIdentityLookup, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	return &GORMIdentityLookup{db: db}, nil
}

// LookupIdentity resolves the exact provider/subject tuple without reading the
// credential_hash column.
func (lookup *GORMIdentityLookup) LookupIdentity(
	ctx context.Context,
	provider string,
	subject string,
) (IdentityMapping, error) {
	if ctx == nil {
		return IdentityMapping{}, fmt.Errorf(
			"%w: context is required",
			ErrInvalidRequest,
		)
	}
	if lookup == nil || lookup.db == nil {
		return IdentityMapping{}, fmt.Errorf(
			"%w: identity lookup is not initialized",
			ErrInvalidState,
		)
	}
	if !validProviderName(provider) ||
		!validIdentitySubject(subject) {
		return IdentityMapping{}, ErrInvalidIdentity
	}

	var row struct {
		Provider string
		Subject  string
		UserID   string
		Status   string
	}
	result := lookup.db.WithContext(ctx).
		Table(UserIdentityTableName).
		Select("provider, subject, user_id, status").
		Where("provider = ? AND subject = ?", provider, subject).
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return IdentityMapping{}, fmt.Errorf(
			"query local identity: %w",
			result.Error,
		)
	}
	if result.RowsAffected != 1 {
		return IdentityMapping{}, ErrIdentityNotFound
	}
	// Some MySQL collations compare text case-insensitively. The external
	// provider/subject tuple is case-sensitive, so reject a row that only
	// matched through database collation.
	if row.Provider != provider || row.Subject != subject {
		return IdentityMapping{}, ErrIdentityNotFound
	}

	mapping := IdentityMapping{
		Provider: row.Provider,
		Subject:  row.Subject,
		UserID:   row.UserID,
		Status:   row.Status,
		Active:   row.Status == defaultActiveStatus,
	}
	if !validIdentityMapping(mapping) {
		return IdentityMapping{}, fmt.Errorf(
			"%w: corrupt identity row",
			ErrInvalidState,
		)
	}
	return mapping, nil
}

// LookupSubject resolves one non-deleted user and maps the conventional
// "active" status into the token module's provider-neutral Active flag.
func (lookup *GORMIdentityLookup) LookupSubject(
	ctx context.Context,
	userID string,
) (Subject, error) {
	if ctx == nil {
		return Subject{}, fmt.Errorf(
			"%w: context is required",
			ErrInvalidRequest,
		)
	}
	if lookup == nil || lookup.db == nil {
		return Subject{}, fmt.Errorf(
			"%w: subject lookup is not initialized",
			ErrInvalidState,
		)
	}
	if !validID(userID) {
		return Subject{}, ErrInvalidSubject
	}

	var row struct {
		ID     string
		Status string
	}
	result := lookup.db.WithContext(ctx).
		Table(UserTableName).
		Select("id, status").
		Where("id = ? AND deleted_at IS NULL", userID).
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return Subject{}, fmt.Errorf(
			"query token subject: %w",
			result.Error,
		)
	}
	if result.RowsAffected != 1 {
		return Subject{}, ErrSubjectNotFound
	}
	if row.ID != userID ||
		!validID(row.ID) ||
		!validText(row.Status, maxIdentityStatusBytes) {
		return Subject{}, fmt.Errorf(
			"%w: corrupt subject row",
			ErrInvalidState,
		)
	}
	return Subject{
		ID:     row.ID,
		Status: row.Status,
		Active: row.Status == defaultActiveStatus,
	}, nil
}
