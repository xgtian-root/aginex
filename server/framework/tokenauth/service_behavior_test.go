package tokenauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestIssuePersistsOnlyKeyedRefreshHashAndValidatesExplicitAccessClaims(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 13, 14, 15, 0, time.UTC)
	clock := newFakeClock(now)
	db := openTokenDatabase(t)
	service := newTestService(t, db, clock, newDeterministicReader(7))
	device := Device{
		ID:       "phone-1",
		Name:     "Alice's phone",
		Platform: "ios",
		Metadata: map[string]string{"appVersion": "1.2.3"},
	}

	pair, err := service.Issue(ctx, "user-1", device)
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken.Type != bearerTokenType {
		t.Fatalf("access type = %q, want Bearer", pair.AccessToken.Type)
	}
	if pair.FamilyID == "" || pair.DeviceID != device.ID {
		t.Fatalf("pair metadata = %+v", pair)
	}
	authenticated, err := service.ValidateAccess(ctx, pair.AccessToken.Value)
	if err != nil {
		t.Fatal(err)
	}
	claims := authenticated.Claims
	if claims.Issuer != service.config.Issuer ||
		claims.Audience != service.config.Audience ||
		claims.Subject != "user-1" ||
		claims.Type != accessTokenType ||
		claims.FamilyID != pair.FamilyID ||
		claims.DeviceID != device.ID ||
		claims.IssuedAt != now.Unix() ||
		claims.ExpiresAt != now.Add(service.config.AccessTTL).Unix() {
		t.Fatalf("access claims = %+v", claims)
	}

	var row refreshTokenRow
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TokenHash == pair.RefreshToken.Value {
		t.Fatal("raw refresh token was persisted as token_hash")
	}
	if len(row.TokenHash) != sha256.Size*2 {
		t.Fatalf("stored hash length = %d, want %d", len(row.TokenHash), sha256.Size*2)
	}
	unkeyed := sha256.Sum256([]byte(pair.RefreshToken.Value))
	if row.TokenHash == hex.EncodeToString(unkeyed[:]) {
		t.Fatal("refresh token used an unkeyed SHA-256 digest")
	}
	if row.TokenHash != service.keyedHash(pair.RefreshToken.Value).databaseValue() {
		t.Fatal("stored hash does not match the configured keyed digest")
	}
	for _, persisted := range []string{
		row.ID,
		row.TokenHash,
		row.FamilyID,
		row.ParentID,
		row.UserID,
		row.DeviceID,
		row.DeviceName,
		row.DevicePlatform,
		row.DeviceMetadata,
		row.Status,
		row.RevokeReason,
	} {
		if strings.Contains(persisted, pair.RefreshToken.Value) {
			t.Fatal("raw refresh token leaked into a persisted text field")
		}
	}

	// Caller mutation after issuance cannot change persisted device metadata.
	device.Metadata["appVersion"] = "mutated"
	session, err := service.store.Lookup(ctx, service.keyedHash(pair.RefreshToken.Value))
	if err != nil {
		t.Fatal(err)
	}
	if session.Device.Metadata["appVersion"] != "1.2.3" {
		t.Fatalf("persisted metadata = %+v, want an immutable copy", session.Device.Metadata)
	}

	secondDB := openTokenDatabase(t)
	second := newTestService(t, secondDB, newFakeClock(now), newDeterministicReader(7))
	deterministic, err := second.Issue(ctx, "user-1", Device{
		ID:       "phone-1",
		Name:     "Alice's phone",
		Platform: "ios",
		Metadata: map[string]string{"appVersion": "1.2.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deterministic != pair {
		t.Fatalf("deterministic issuance differs:\nfirst=%+v\nsecond=%+v", pair, deterministic)
	}
}

func TestRefreshRotatesOncePreservesDeviceAndReplayRevokesFamily(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 7, 31, 14, 0, 0, 0, time.UTC))
	db := openTokenDatabase(t)
	service := newTestService(t, db, clock, newDeterministicReader(30))
	initial, err := service.Issue(ctx, "user-1", Device{
		ID:       "tablet-1",
		Platform: "android",
		Metadata: map[string]string{"build": "42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	rotated, err := service.Refresh(ctx, initial.RefreshToken.Value)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.FamilyID != initial.FamilyID ||
		rotated.DeviceID != initial.DeviceID ||
		rotated.RefreshToken.Value == initial.RefreshToken.Value {
		t.Fatalf("rotated pair = %+v, initial = %+v", rotated, initial)
	}

	var rows []refreshTokenRow
	if err := db.Order("created_at, id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("refresh rows = %d, want 2", len(rows))
	}
	var used, active refreshTokenRow
	for _, row := range rows {
		switch row.Status {
		case RefreshStatusUsed:
			used = row
		case RefreshStatusActive:
			active = row
		}
	}
	if used.ID == "" || used.UsedAt == nil || active.ParentID != used.ID {
		t.Fatalf("rotation lineage rows = %+v", rows)
	}
	activeSession, err := active.session()
	if err != nil {
		t.Fatal(err)
	}
	if activeSession.Device.Metadata["build"] != "42" {
		t.Fatalf("rotated metadata = %+v", activeSession.Device.Metadata)
	}

	if _, err := service.Refresh(ctx, initial.RefreshToken.Value); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("old-token replay error = %v, want ErrRefreshReplay", err)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("rotated token after replay error = %v, want ErrRefreshRevoked", err)
	}
}

func TestAccessValidationRejectsTamperingTrustMismatchExpiryAndInactiveSubject(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC))
	db := openTokenDatabase(t)
	subjects := &staticSubjects{subjects: map[string]Subject{
		"user-1": {ID: "user-1", Status: "active", Active: true},
	}}
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig()
	service, err := New(config, Dependencies{
		Store:    store,
		Subjects: subjects,
		Clock:    clock,
		Random:   newDeterministicReader(60),
	})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := service.Issue(ctx, "user-1", Device{ID: "browser-1"})
	if err != nil {
		t.Fatal(err)
	}

	tampered := pair.AccessToken.Value
	middle := len(tampered) / 2
	replacement := byte('A')
	if tampered[middle] == replacement {
		replacement = 'B'
	}
	tampered = tampered[:middle] + string(replacement) + tampered[middle+1:]
	if _, err := service.ValidateAccess(ctx, tampered); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("tampered access error = %v, want ErrInvalidAccess", err)
	}

	otherConfig := config
	otherConfig.Audience = "different-api"
	other, err := New(otherConfig, Dependencies{
		Store:    store,
		Subjects: subjects,
		Clock:    clock,
		Random:   newDeterministicReader(90),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ValidateAccess(ctx, pair.AccessToken.Value); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("audience mismatch error = %v, want ErrInvalidAccess", err)
	}

	clock.Advance(config.AccessTTL)
	if _, err := service.ValidateAccess(ctx, pair.AccessToken.Value); !errors.Is(err, ErrAccessExpired) {
		t.Fatalf("expired access error = %v, want ErrAccessExpired", err)
	}

	clock.Advance(-config.AccessTTL)
	subjects.mu.Lock()
	subjects.subjects["user-1"] = Subject{ID: "user-1", Status: "suspended", Active: false}
	subjects.mu.Unlock()
	if _, err := service.ValidateAccess(ctx, pair.AccessToken.Value); !errors.Is(err, ErrSubjectInactive) {
		t.Fatalf("inactive subject error = %v, want ErrSubjectInactive", err)
	}
}

func TestRefreshExpiryAndInactiveSubjectRevokeStoredToken(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		ctx := context.Background()
		clock := newFakeClock(time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC))
		db := openTokenDatabase(t)
		service := newTestService(t, db, clock, newDeterministicReader(100))
		pair, err := service.Issue(ctx, "user-1", Device{ID: "expired-device"})
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(service.config.RefreshTTL)
		if _, err := service.Refresh(ctx, pair.RefreshToken.Value); !errors.Is(err, ErrRefreshExpired) {
			t.Fatalf("expired refresh error = %v, want ErrRefreshExpired", err)
		}
		assertRefreshState(t, db, pair.FamilyID, RefreshStatusRevoked, "expired")
	})

	t.Run("inactive subject", func(t *testing.T) {
		ctx := context.Background()
		clock := newFakeClock(time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC))
		db := openTokenDatabase(t)
		subjects := &staticSubjects{subjects: map[string]Subject{
			"user-1": {ID: "user-1", Active: true},
		}}
		store, err := NewGORMStore(db)
		if err != nil {
			t.Fatal(err)
		}
		service, err := New(testConfig(), Dependencies{
			Store:    store,
			Subjects: subjects,
			Clock:    clock,
			Random:   newDeterministicReader(110),
		})
		if err != nil {
			t.Fatal(err)
		}
		pair, err := service.Issue(ctx, "user-1", Device{ID: "inactive-device"})
		if err != nil {
			t.Fatal(err)
		}
		subjects.mu.Lock()
		subjects.subjects["user-1"] = Subject{ID: "user-1", Active: false}
		subjects.mu.Unlock()
		if _, err := service.Refresh(ctx, pair.RefreshToken.Value); !errors.Is(err, ErrSubjectInactive) {
			t.Fatalf("inactive refresh error = %v, want ErrSubjectInactive", err)
		}
		assertRefreshState(t, db, pair.FamilyID, RefreshStatusRevoked, "subject_inactive")
	})

	t.Run("missing subject", func(t *testing.T) {
		ctx := context.Background()
		clock := newFakeClock(time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC))
		db := openTokenDatabase(t)
		subjects := &staticSubjects{subjects: map[string]Subject{
			"user-1": {ID: "user-1", Active: true},
		}}
		store, err := NewGORMStore(db)
		if err != nil {
			t.Fatal(err)
		}
		service, err := New(testConfig(), Dependencies{
			Store:    store,
			Subjects: subjects,
			Clock:    clock,
			Random:   newDeterministicReader(120),
		})
		if err != nil {
			t.Fatal(err)
		}
		pair, err := service.Issue(ctx, "user-1", Device{ID: "deleted-user-device"})
		if err != nil {
			t.Fatal(err)
		}
		subjects.mu.Lock()
		delete(subjects.subjects, "user-1")
		subjects.mu.Unlock()
		if _, err := service.Refresh(ctx, pair.RefreshToken.Value); !errors.Is(err, ErrSubjectNotFound) {
			t.Fatalf("missing subject refresh error = %v, want ErrSubjectNotFound", err)
		}
		assertRefreshState(t, db, pair.FamilyID, RefreshStatusRevoked, "subject_inactive")
	})
}

func TestFamilyDeviceAndUserRevocationAreScoped(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC))
	db := openTokenDatabase(t)
	subjects := &staticSubjects{subjects: map[string]Subject{
		"user-1": {ID: "user-1", Active: true},
		"user-2": {ID: "user-2", Active: true},
	}}
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(testConfig(), Dependencies{
		Store:    store,
		Subjects: subjects,
		Clock:    clock,
		Random:   newDeterministicReader(130),
	})
	if err != nil {
		t.Fatal(err)
	}
	first := mustIssue(t, service, "user-1", Device{ID: "phone"})
	second := mustIssue(t, service, "user-1", Device{ID: "phone"})
	third := mustIssue(t, service, "user-1", Device{ID: "laptop"})
	otherUser := mustIssue(t, service, "user-2", Device{ID: "phone"})

	count, err := service.RevokeFamily(ctx, "user-1", first.FamilyID)
	if err != nil || count != 1 {
		t.Fatalf("revoke family count=%d error=%v", count, err)
	}
	if _, err := service.Refresh(ctx, first.RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("family refresh error = %v, want revoked", err)
	}

	count, err = service.RevokeDevice(ctx, "user-1", "phone")
	if err != nil || count != 1 {
		t.Fatalf("revoke device count=%d error=%v", count, err)
	}
	if _, err := service.Refresh(ctx, second.RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("device refresh error = %v, want revoked", err)
	}

	count, err = service.RevokeUser(ctx, "user-1")
	if err != nil || count != 1 {
		t.Fatalf("revoke user count=%d error=%v", count, err)
	}
	if _, err := service.Refresh(ctx, third.RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("all-user refresh error = %v, want revoked", err)
	}
	if _, err := service.Refresh(ctx, otherUser.RefreshToken.Value); err != nil {
		t.Fatalf("other user's refresh was affected: %v", err)
	}
}

func TestConstructorInputsAndEntropyFailuresFailClosed(t *testing.T) {
	db := openTokenDatabase(t)
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	subjects := &staticSubjects{subjects: map[string]Subject{
		"user-1": {ID: "user-1", Active: true},
	}}
	valid := testConfig()
	tests := []struct {
		name   string
		mutate func(*Config, *Dependencies)
	}{
		{name: "issuer", mutate: func(config *Config, _ *Dependencies) { config.Issuer = "" }},
		{name: "audience", mutate: func(config *Config, _ *Dependencies) { config.Audience = "" }},
		{name: "access ttl", mutate: func(config *Config, _ *Dependencies) { config.AccessTTL = 0 }},
		{name: "subsecond access ttl", mutate: func(config *Config, _ *Dependencies) {
			config.AccessTTL = time.Second - time.Nanosecond
		}},
		{name: "refresh ttl", mutate: func(config *Config, _ *Dependencies) { config.RefreshTTL = time.Second }},
		{name: "access key", mutate: func(config *Config, _ *Dependencies) { config.AccessSigningKey = []byte("short") }},
		{name: "refresh key", mutate: func(config *Config, _ *Dependencies) { config.RefreshHashKey = []byte("short") }},
		{name: "same keys", mutate: func(config *Config, _ *Dependencies) {
			config.RefreshHashKey = append([]byte(nil), config.AccessSigningKey...)
		}},
		{name: "store", mutate: func(_ *Config, dependencies *Dependencies) { dependencies.Store = nil }},
		{name: "subjects", mutate: func(_ *Config, dependencies *Dependencies) { dependencies.Subjects = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			dependencies := Dependencies{
				Store:    store,
				Subjects: subjects,
				Clock:    newFakeClock(time.Now()),
				Random:   newDeterministicReader(1),
			}
			test.mutate(&config, &dependencies)
			if _, err := New(config, dependencies); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	service, err := New(valid, Dependencies{
		Store:    store,
		Subjects: subjects,
		Clock:    newFakeClock(time.Now()),
		Random:   errorReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Issue(context.Background(), "user-1", Device{ID: "phone"}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("entropy error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := service.Issue(nil, "user-1", Device{ID: "phone"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context error = %v, want ErrInvalidRequest", err)
	}
	regular := newTestService(t, db, newFakeClock(time.Now()), newDeterministicReader(2))
	if _, err := regular.Issue(context.Background(), "", Device{ID: "phone"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty user error = %v, want ErrInvalidRequest", err)
	}
	if _, err := regular.Issue(context.Background(), "user-1", Device{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty device error = %v, want ErrInvalidRequest", err)
	}
	if _, err := regular.Refresh(context.Background(), "not-a-token"); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("malformed refresh error = %v, want ErrRefreshInvalid", err)
	}
	brokenClock, err := New(valid, Dependencies{
		Store:    store,
		Subjects: subjects,
		Clock:    ClockFunc(func() time.Time { return time.Time{} }),
		Random:   newDeterministicReader(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := brokenClock.Issue(context.Background(), "user-1", Device{ID: "phone"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("zero clock error = %v, want ErrInvalidState", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func testConfig() Config {
	return Config{
		Issuer:           "https://issuer.example.test",
		Audience:         "posta-api",
		AccessTTL:        5 * time.Minute,
		RefreshTTL:       30 * 24 * time.Hour,
		AccessSigningKey: []byte("access-signing-key-with-at-least-32-bytes"),
		RefreshHashKey:   []byte("refresh-hashing-key-with-at-least-32-bytes"),
	}
}

func mustIssue(t *testing.T, service *Service, userID string, device Device) TokenPair {
	t.Helper()
	pair, err := service.Issue(context.Background(), userID, device)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func assertRefreshState(
	t *testing.T,
	db interface {
		Where(any, ...any) *gorm.DB
	},
	familyID string,
	status string,
	reason string,
) {
	t.Helper()
	var row refreshTokenRow
	result := db.Where("family_id = ?", familyID).Take(&row)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if row.Status != status || row.RevokeReason != reason || row.RevokedAt == nil {
		t.Fatalf("refresh row = %+v, want status=%q reason=%q", row, status, reason)
	}
}
