package app

import (
	"testing"

	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
)

func TestHasActiveAdministratorRequiresUsablePasswordIdentity(t *testing.T) {
	db := openBootstrapDatabase(t)
	if active, err := HasActiveAdministrator(t.Context(), db); err != nil {
		t.Fatal(err)
	} else if active {
		t.Fatal("empty installation reported an active administrator")
	}

	const email = "admin@example.com"
	if err := Bootstrap(t.Context(), db, config.Bootstrap{
		AdminEmail:    email,
		AdminPassword: "correct bootstrap password",
	}); err != nil {
		t.Fatal(err)
	}
	if active, err := HasActiveAdministrator(t.Context(), db); err != nil {
		t.Fatal(err)
	} else if !active {
		t.Fatal("bootstrapped installation has no active administrator")
	}
	if active, err := HasActiveAdministratorCredentials(
		t.Context(),
		db,
		email,
		"correct bootstrap password",
	); err != nil {
		t.Fatal(err)
	} else if !active {
		t.Fatal("submitted administrator credentials were not verified")
	}
	if active, err := HasActiveAdministratorCredentials(
		t.Context(),
		db,
		email,
		"incorrect bootstrap password",
	); err != nil {
		t.Fatal(err)
	} else if active {
		t.Fatal("incorrect administrator password was accepted")
	}

	if err := db.Model(&domain.UserIdentity{}).
		Where("provider = ? AND subject = ?", domain.IdentityProviderPassword, email).
		Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if active, err := HasActiveAdministrator(t.Context(), db); err != nil {
		t.Fatal(err)
	} else if active {
		t.Fatal("disabled password identity reported an active administrator")
	}

	if err := db.Model(&domain.UserIdentity{}).
		Where("provider = ? AND subject = ?", domain.IdentityProviderPassword, email).
		Updates(map[string]any{
			"status":          domain.IdentityStatusActive,
			"credential_hash": "not-an-argon2-hash",
		}).Error; err != nil {
		t.Fatal(err)
	}
	if active, err := HasActiveAdministrator(t.Context(), db); err != nil {
		t.Fatal(err)
	} else if active {
		t.Fatal("corrupt password hash reported an active administrator")
	}
}
