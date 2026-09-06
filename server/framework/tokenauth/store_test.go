package tokenauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRotationInsertFailureRollsBackTokenConsumption(t *testing.T) {
	ctx := context.Background()
	db := openTokenDatabase(t)
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 19, 0, 0, 0, time.UTC)
	firstHash := testTokenHash(1)
	secondHash := testTokenHash(2)
	first, err := store.Create(ctx, NewRefreshToken{
		ID:        "token-1",
		FamilyID:  "family-1",
		UserID:    "user-1",
		Hash:      firstHash,
		Device:    Device{ID: "device-1"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, NewRefreshToken{
		ID:        "token-2",
		FamilyID:  "family-2",
		UserID:    "user-1",
		Hash:      secondHash,
		Device:    Device{ID: "device-2"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = store.Rotate(ctx, firstHash, RefreshReplacement{
		ID:        "token-2",
		Hash:      testTokenHash(3),
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}, now)
	if err == nil {
		t.Fatal("rotation with a duplicate child ID unexpectedly succeeded")
	}
	current, err := store.Lookup(ctx, firstHash)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != first.ID || current.Status != RefreshStatusActive || current.UsedAt != nil {
		t.Fatalf("rolled-back current token = %+v, want original active token", current)
	}
}

func TestStoreFailsClosedForCorruptRowsAndInvalidCalls(t *testing.T) {
	ctx := context.Background()
	db := openTokenDatabase(t)
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGORMStore(nil); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil database error = %v, want ErrDatabaseRequired", err)
	}
	if _, err := store.Lookup(nil, testTokenHash(1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil lookup context error = %v, want ErrInvalidRequest", err)
	}
	if _, err := store.Lookup(ctx, TokenHash{}); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("empty hash error = %v, want ErrRefreshInvalid", err)
	}

	now := time.Date(2026, 7, 31, 20, 0, 0, 0, time.UTC)
	hash := testTokenHash(9)
	if _, err := store.Create(ctx, NewRefreshToken{
		ID:        "token-corrupt",
		FamilyID:  "family-corrupt",
		UserID:    "user-1",
		Hash:      hash,
		Device:    Device{ID: "device-corrupt"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&refreshTokenRow{}).
		Where("id = ?", "token-corrupt").
		UpdateColumn("device_metadata", "{").
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, hash); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("corrupt row error = %v, want ErrInvalidState", err)
	}
}

func testTokenHash(seed byte) TokenHash {
	var hash TokenHash
	for index := range hash {
		hash[index] = seed + byte(index)
	}
	return hash
}
