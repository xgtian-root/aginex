package tokenauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGORMIdentityLookupAndAuthenticatorResolveOnlyActiveLocalUser(
	t *testing.T,
) {
	db := openTokenDatabase(t)
	createIdentityFixtureSchema(t, db)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	if err := db.Exec(`
		INSERT INTO users (id, status, deleted_at)
		VALUES (?, ?, NULL)
	`, "user-1", defaultActiveStatus).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO user_identities (
			id, user_id, provider, subject, credential_hash,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"identity-1",
		"user-1",
		"apple",
		"apple-subject-1",
		"must-not-be-read",
		defaultActiveStatus,
		now,
		now,
	).Error; err != nil {
		t.Fatal(err)
	}

	lookup, err := NewGORMIdentityLookup(db)
	if err != nil {
		t.Fatal(err)
	}
	providerAttributes := map[string]string{"email": "alice@example.test"}
	authenticator, err := NewIdentityAuthenticator(
		IdentityAuthenticationDependencies{
			Provider: &testIdentityProvider{
				name: "apple",
				identity: ExternalIdentity{
					Provider:   "apple",
					Subject:    "apple-subject-1",
					Attributes: providerAttributes,
				},
			},
			Identities: lookup,
			Subjects:   lookup,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := authenticator.Authenticate(
		context.Background(),
		testIdentityCredential{kind: "apple"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.Mapping.UserID != "user-1" ||
		!authenticated.Mapping.Active ||
		authenticated.Subject.ID != "user-1" ||
		!authenticated.Subject.Active {
		t.Fatalf("authenticated identity = %+v", authenticated)
	}
	providerAttributes["email"] = "mutated@example.test"
	if authenticated.External.Attributes["email"] !=
		"alice@example.test" {
		t.Fatal("provider attributes were not copied")
	}

	if err := db.Table(UserIdentityTableName).
		Where("id = ?", "identity-1").
		UpdateColumn("status", "disabled").
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Authenticate(
		context.Background(),
		testIdentityCredential{kind: "apple"},
	); !errors.Is(err, ErrIdentityInactive) {
		t.Fatalf(
			"inactive identity error = %v, want ErrIdentityInactive",
			err,
		)
	}

	if err := db.Table(UserIdentityTableName).
		Where("id = ?", "identity-1").
		UpdateColumn("status", defaultActiveStatus).
		Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table(UserTableName).
		Where("id = ?", "user-1").
		UpdateColumn("status", "suspended").
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Authenticate(
		context.Background(),
		testIdentityCredential{kind: "apple"},
	); !errors.Is(err, ErrSubjectInactive) {
		t.Fatalf(
			"inactive subject error = %v, want ErrSubjectInactive",
			err,
		)
	}

	if err := db.Table(UserTableName).
		Where("id = ?", "user-1").
		UpdateColumn("deleted_at", now).
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := lookup.LookupSubject(
		context.Background(),
		"user-1",
	); !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf(
			"deleted subject error = %v, want ErrSubjectNotFound",
			err,
		)
	}
}

func TestIdentityAuthenticatorFailsClosedForConfusedOrInvalidAdapters(
	t *testing.T,
) {
	validMapping := &testIdentityLookup{
		mapping: IdentityMapping{
			Provider: "apple",
			Subject:  "apple-subject-1",
			UserID:   "user-1",
			Status:   defaultActiveStatus,
			Active:   true,
		},
		subject: Subject{
			ID:     "user-1",
			Status: defaultActiveStatus,
			Active: true,
		},
	}
	validProvider := &testIdentityProvider{
		name: "apple",
		identity: ExternalIdentity{
			Provider: "apple",
			Subject:  "apple-subject-1",
		},
	}

	t.Run("typed nil dependencies", func(t *testing.T) {
		var provider *testIdentityProvider
		if _, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider:   provider,
				Identities: validMapping,
				Subjects:   validMapping,
			},
		); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf(
				"typed nil provider error = %v, want ErrInvalidConfig",
				err,
			)
		}

		var identities *testIdentityLookup
		if _, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider:   validProvider,
				Identities: identities,
				Subjects:   validMapping,
			},
		); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf(
				"typed nil lookup error = %v, want ErrInvalidConfig",
				err,
			)
		}
	})

	t.Run("typed nil credential", func(t *testing.T) {
		authenticator, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider:   validProvider,
				Identities: validMapping,
				Subjects:   validMapping,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		var credential *testIdentityCredential
		if _, err := authenticator.Authenticate(
			context.Background(),
			credential,
		); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf(
				"typed nil credential error = %v, want ErrInvalidRequest",
				err,
			)
		}
	})

	t.Run("provider mismatch", func(t *testing.T) {
		authenticator, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider: &testIdentityProvider{
					name: "apple",
					identity: ExternalIdentity{
						Provider: "sms",
						Subject:  "sms-subject-1",
					},
				},
				Identities: validMapping,
				Subjects:   validMapping,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authenticator.Authenticate(
			context.Background(),
			testIdentityCredential{kind: "apple"},
		); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf(
				"provider mismatch error = %v, want ErrInvalidIdentity",
				err,
			)
		}
	})

	t.Run("mapping mismatch", func(t *testing.T) {
		mismatch := *validMapping
		mismatch.mapping.Subject = "different-subject"
		authenticator, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider:   validProvider,
				Identities: &mismatch,
				Subjects:   validMapping,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authenticator.Authenticate(
			context.Background(),
			testIdentityCredential{kind: "apple"},
		); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf(
				"mapping mismatch error = %v, want ErrInvalidIdentity",
				err,
			)
		}
	})

	t.Run("credential attribute", func(t *testing.T) {
		authenticator, err := NewIdentityAuthenticator(
			IdentityAuthenticationDependencies{
				Provider: &testIdentityProvider{
					name: "apple",
					identity: ExternalIdentity{
						Provider: "apple",
						Subject:  "apple-subject-1",
						Attributes: map[string]string{
							"id_token": "raw-provider-secret",
						},
					},
				},
				Identities: validMapping,
				Subjects:   validMapping,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authenticator.Authenticate(
			context.Background(),
			testIdentityCredential{kind: "apple"},
		); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf(
				"credential attribute error = %v, want ErrInvalidIdentity",
				err,
			)
		}
	})

	if _, err := NewGORMIdentityLookup(nil); !errors.Is(
		err,
		ErrDatabaseRequired,
	) {
		t.Fatalf(
			"nil GORM database error = %v, want ErrDatabaseRequired",
			err,
		)
	}
}

func createIdentityFixtureSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE user_identities (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			subject TEXT NOT NULL,
			credential_hash TEXT,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE (provider, subject)
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

type testIdentityCredential struct {
	kind string
}

func (credential testIdentityCredential) CredentialKind() string {
	return credential.kind
}

type testIdentityProvider struct {
	name     string
	identity ExternalIdentity
	err      error
}

func (provider *testIdentityProvider) Name() string {
	return provider.name
}

func (provider *testIdentityProvider) Authenticate(
	_ context.Context,
	_ IdentityCredential,
) (ExternalIdentity, error) {
	return provider.identity, provider.err
}

type testIdentityLookup struct {
	mapping IdentityMapping
	subject Subject
	err     error
}

func (lookup *testIdentityLookup) LookupIdentity(
	_ context.Context,
	_ string,
	_ string,
) (IdentityMapping, error) {
	return lookup.mapping, lookup.err
}

func (lookup *testIdentityLookup) LookupSubject(
	_ context.Context,
	_ string,
) (Subject, error) {
	return lookup.subject, lookup.err
}
