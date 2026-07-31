package module

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type resourcePersistenceModel struct {
	ID     string
	Secret string
}

type resourceCreateRequest struct {
	Name string
}

type resourceResponse struct {
	ID string
}

type testModule struct {
	name     string
	register func(*Registry) error
}

func (m testModule) Name() string {
	return m.name
}

func (m testModule) Register(registry *Registry) error {
	if m.register == nil {
		return nil
	}
	return m.register(registry)
}

func TestRegisterModulesUsesDeterministicOrder(t *testing.T) {
	registry := NewRegistry()
	var calls []string

	modules := []Module{
		testModule{name: "zeta", register: func(registry *Registry) error {
			calls = append(calls, "zeta")
			return registry.RegisterPermission(PermissionDefinition{
				Code:        "zeta:read",
				Description: "Read zeta resources",
			})
		}},
		testModule{name: "alpha", register: func(registry *Registry) error {
			calls = append(calls, "alpha")
			return registry.RegisterPermission(PermissionDefinition{
				Code:        "alpha:read",
				Description: "Read alpha resources",
			})
		}},
	}

	if err := registry.RegisterModules(modules...); err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("registration order = %v, want %v", calls, want)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(registry.Modules(), want) {
		t.Fatalf("module snapshot = %v, want %v", registry.Modules(), want)
	}
	permissions := registry.Permissions()
	if got, want := []string{permissions[0].Code, permissions[1].Code}, []string{"alpha:read", "zeta:read"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("permission order = %v, want %v", got, want)
	}
}

func TestRegisterModulesRejectsDuplicatesBeforeCallingModules(t *testing.T) {
	registry := NewRegistry()
	called := false
	first := testModule{name: "postmarks", register: func(*Registry) error {
		called = true
		return nil
	}}
	second := testModule{name: "postmarks"}

	err := registry.RegisterModules(first, second)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("error = %v, want ErrDuplicate", err)
	}
	if called {
		t.Fatal("module registration ran before duplicate names were rejected")
	}
	if got := registry.Modules(); len(got) != 0 {
		t.Fatalf("registry changed after rejected modules: %v", got)
	}
}

func TestRegisterModulesIsAtomic(t *testing.T) {
	registry := NewRegistry()
	registerErr := errors.New("module setup failed")
	broken := testModule{name: "broken", register: func(registry *Registry) error {
		if err := registry.RegisterPermission(PermissionDefinition{
			Code:        "broken:read",
			Description: "Read broken resources",
		}); err != nil {
			return err
		}
		return registerErr
	}}

	err := registry.RegisterModules(broken)
	if !errors.Is(err, registerErr) {
		t.Fatalf("error = %v, want %v", err, registerErr)
	}
	if got := registry.Modules(); len(got) != 0 {
		t.Fatalf("registry retained a failed module: %v", got)
	}
	if got := registry.Permissions(); len(got) != 0 {
		t.Fatalf("registry retained failed module permissions: %v", got)
	}
}

func TestOperationAuthorizationMustBeExplicit(t *testing.T) {
	tests := []struct {
		name      string
		operation OperationDefinition
		wantErr   bool
	}{
		{
			name: "public",
			operation: OperationDefinition{
				ID:     "healthLive",
				Method: MethodGet,
				Path:   "/api/v1/health/live",
				Public: true,
			},
		},
		{
			name: "protected",
			operation: OperationDefinition{
				ID:             "listPostmarks",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks",
				Authentication: "testSession",
				Permission:     "postmarks:read",
				Policy:         PolicyRef("postmarks.scope"),
			},
		},
		{
			name: "missing authorization",
			operation: OperationDefinition{
				ID:     "listPostmarks",
				Method: MethodGet,
				Path:   "/api/v1/postmarks",
			},
			wantErr: true,
		},
		{
			name: "permission without policy",
			operation: OperationDefinition{
				ID:             "listPostmarks",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks",
				Authentication: "testSession",
				Permission:     "postmarks:read",
			},
			wantErr: true,
		},
		{
			name: "policy without permission",
			operation: OperationDefinition{
				ID:             "listPostmarks",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks",
				Authentication: "testSession",
				Policy:         PolicyRef("postmarks.scope"),
			},
			wantErr: true,
		},
		{
			name: "ambiguous public and protected",
			operation: OperationDefinition{
				ID:             "listPostmarks",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks",
				Public:         true,
				Authentication: "testSession",
				Permission:     "postmarks:read",
				Policy:         PolicyRef("postmarks.scope"),
			},
			wantErr: true,
		},
		{
			name: "noncanonical API path",
			operation: OperationDefinition{
				ID:     "escapedPath",
				Method: MethodGet,
				Path:   "/api/v1/../internal",
				Public: true,
			},
			wantErr: true,
		},
		{
			name: "raw Gin parameter",
			operation: OperationDefinition{
				ID:     "rawGinParameter",
				Method: MethodGet,
				Path:   "/api/v1/postmarks/:id",
				Public: true,
			},
			wantErr: true,
		},
		{
			name: "non-terminal catch all",
			operation: OperationDefinition{
				ID:     "nonTerminalCatchAll",
				Method: MethodGet,
				Path:   "/api/v1/postmarks/{path+}/metadata",
				Public: true,
			},
			wantErr: true,
		},
		{
			name: "encoded segment",
			operation: OperationDefinition{
				ID:     "encodedSegment",
				Method: MethodGet,
				Path:   "/api/v1/postmarks/%2e%2e/internal",
				Public: true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			err := registry.RegisterOperation(tt.operation)
			if tt.wantErr && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationRequestProtectionMustBeExplicitAndValid(t *testing.T) {
	protectedWrite := func() OperationDefinition {
		return OperationDefinition{
			ID:             "createPostmark",
			Method:         MethodPost,
			Path:           "/api/v1/postmarks",
			Authentication: "testSession",
			Permission:     "postmarks:create",
			Policy:         PolicyRef("postmarks.scope"),
		}
	}
	publicWrite := func() OperationDefinition {
		return OperationDefinition{
			ID:     "login",
			Method: MethodPost,
			Path:   "/api/v1/auth/login",
			Public: true,
		}
	}
	validActorLimit := RateLimitPolicy{
		Namespace: "postmarks.write.actor",
		Subject:   RateLimitByActor,
		Limit:     5,
		Window:    time.Minute,
	}
	validIPLimit := RateLimitPolicy{
		Namespace: "auth.login.ip",
		Subject:   RateLimitByIP,
		Limit:     10,
		Window:    time.Minute,
	}

	tests := []struct {
		name      string
		operation OperationDefinition
		wantErr   bool
	}{
		{
			name:      "protected write without request protection",
			operation: protectedWrite(),
		},
		{
			name: "optional idempotency",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.Idempotency = IdempotencyOptional
				return result
			}(),
		},
		{
			name: "unknown idempotency policy",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.Idempotency = "required"
				return result
			}(),
			wantErr: true,
		},
		{
			name: "idempotency on read",
			operation: OperationDefinition{
				ID:             "readPostmark",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks/{id}",
				Authentication: "testSession",
				Permission:     "postmarks:read",
				Policy:         PolicyRef("postmarks.scope"),
				Idempotency:    IdempotencyOptional,
			},
			wantErr: true,
		},
		{
			name: "idempotency on public write",
			operation: func() OperationDefinition {
				result := publicWrite()
				result.Idempotency = IdempotencyOptional
				return result
			}(),
			wantErr: true,
		},
		{
			name: "public IP rate limit",
			operation: func() OperationDefinition {
				result := publicWrite()
				result.RateLimit = validIPLimit
				return result
			}(),
		},
		{
			name: "protected actor rate limit",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.RateLimit = validActorLimit
				return result
			}(),
		},
		{
			name: "public actor rate limit",
			operation: func() OperationDefinition {
				result := publicWrite()
				result.RateLimit = validActorLimit
				return result
			}(),
			wantErr: true,
		},
		{
			name: "missing rate limit namespace",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.RateLimit = validActorLimit
				result.RateLimit.Namespace = ""
				return result
			}(),
			wantErr: true,
		},
		{
			name: "unknown rate limit subject",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.RateLimit = validActorLimit
				result.RateLimit.Subject = "email"
				return result
			}(),
			wantErr: true,
		},
		{
			name: "zero rate limit capacity",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.RateLimit = validActorLimit
				result.RateLimit.Limit = 0
				return result
			}(),
			wantErr: true,
		},
		{
			name: "too short rate limit window",
			operation: func() OperationDefinition {
				result := protectedWrite()
				result.RateLimit = validActorLimit
				result.RateLimit.Window = time.Microsecond
				return result
			}(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			err := registry.RegisterOperation(test.operation)
			if test.wantErr && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if !test.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationRequestProtectionIsNormalizedInSnapshots(t *testing.T) {
	registry := NewRegistry()
	operation := OperationDefinition{
		ID:             "createPostmark",
		Method:         MethodPost,
		Path:           "/api/v1/postmarks",
		Authentication: "testSession",
		Permission:     "postmarks:create",
		Policy:         PolicyRef("postmarks.scope"),
		Idempotency:    " optional ",
		RateLimit: RateLimitPolicy{
			Namespace: " postmarks.write.actor ",
			Subject:   " actor ",
			Limit:     5,
			Window:    time.Minute,
		},
	}
	if err := registry.RegisterOperation(operation); err != nil {
		t.Fatal(err)
	}
	registered := registry.Operations()
	if len(registered) != 1 {
		t.Fatalf("operations = %#v", registered)
	}
	if registered[0].Idempotency != IdempotencyOptional ||
		registered[0].RateLimit.Namespace != "postmarks.write.actor" ||
		registered[0].RateLimit.Subject != RateLimitByActor {
		t.Fatalf("normalized operation = %#v", registered[0])
	}
}

func TestAPIContractCannotRedeclareIdempotencyHeader(t *testing.T) {
	registry := NewRegistry()
	maxLength := 200
	if err := registry.RegisterPermission(PermissionDefinition{
		Code:        "postmarks:create",
		Description: "Create postmarks",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterOperation(OperationDefinition{
		ID:             "createPostmark",
		Method:         MethodPost,
		Path:           "/api/v1/postmarks",
		Authentication: "testSession",
		Permission:     "postmarks:create",
		Policy:         PolicyRef("postmarks.scope"),
		Idempotency:    IdempotencyOptional,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterAPIContract(APIContract{
		OperationID:   "createPostmark",
		Summary:       "Create a postmark",
		Tag:           "Postmarks",
		SuccessStatus: 201,
		Parameters: []ParameterDefinition{{
			Name:        "Idempotency-Key",
			In:          ParameterHeader,
			Description: "Reserved framework request header.",
			Type:        ParameterString,
			MaxLength:   &maxLength,
		}},
		ErrorStatuses: []int{400, 401, 403, 409, 503},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestOperationRouteShapeRejectsDifferentParameterNames(t *testing.T) {
	registry := NewRegistry()
	for _, operation := range []OperationDefinition{
		{
			ID:     "readPostmark",
			Method: MethodGet,
			Path:   "/api/v1/postmarks/{id}",
			Public: true,
		},
		{
			ID:     "readPostmarkBySlug",
			Method: MethodGet,
			Path:   "/api/v1/postmarks/{slug}",
			Public: true,
		},
	} {
		err := registry.RegisterOperation(operation)
		if operation.ID == "readPostmark" && err != nil {
			t.Fatal(err)
		}
		if operation.ID == "readPostmarkBySlug" &&
			!errors.Is(err, ErrDuplicate) {
			t.Fatalf("error = %v, want ErrDuplicate", err)
		}
	}
}

func TestModuleCannotIgnoreARejectedDefinition(t *testing.T) {
	registry := NewRegistry()
	unsafe := testModule{name: "unsafe", register: func(registry *Registry) error {
		_ = registry.RegisterOperation(OperationDefinition{
			ID:     "unprotected",
			Method: MethodGet,
			Path:   "/api/v1/unprotected",
		})
		return nil
	}}

	err := registry.RegisterModules(unsafe)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if got := registry.Modules(); len(got) != 0 {
		t.Fatalf("registry changed after ignored registration error: %v", got)
	}
}

func TestProtectedOperationRequiresRegisteredPermission(t *testing.T) {
	registry := NewRegistry()
	postmarks := testModule{name: "postmarks", register: func(registry *Registry) error {
		return registry.RegisterOperation(OperationDefinition{
			ID:             "listPostmarks",
			Method:         MethodGet,
			Path:           "/api/v1/postmarks",
			Authentication: "testSession",
			Permission:     "postmarks:read",
			Policy:         PolicyRef("postmarks.scope"),
		})
	}}

	err := registry.RegisterModules(postmarks)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if got := registry.Modules(); len(got) != 0 {
		t.Fatalf("registry changed after unresolved permission: %v", got)
	}
}

func TestResourceDefinitionRequiresExplicitAuthorizationDTOsAndFieldClassification(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterPermission(PermissionDefinition{
		Code:        "postmarks:create",
		Description: "Create postmarks",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterOperation(OperationDefinition{
		ID:             "createPostmark",
		Method:         MethodPost,
		Path:           "/api/v1/postmarks",
		Authentication: "testSession",
		Permission:     "postmarks:create",
		Policy:         PolicyRef("owner"),
	}); err != nil {
		t.Fatal(err)
	}
	definition := ResourceDefinition{
		Name:            "postmarks",
		Model:           reflect.TypeFor[*resourcePersistenceModel](),
		Ownership:       OwnershipOwner,
		Policy:          PolicyRef("owner"),
		AuditableFields: []string{"title", "status"},
		SensitiveFields: []string{"private_note"},
		Operations: []ResourceOperationDefinition{{
			OperationID: "createPostmark",
			RequestDTO:  reflect.TypeFor[resourceCreateRequest](),
			ResponseDTO: reflect.TypeFor[resourceResponse](),
		}},
	}
	if err := registry.RegisterResource(definition); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(); err != nil {
		t.Fatal(err)
	}

	definition.AuditableFields[0] = "mutated_by_caller"
	definition.Operations[0].OperationID = "mutatedByCaller"
	first := registry.Resources()
	if len(first) != 1 {
		t.Fatalf("resources = %#v", first)
	}
	first[0].SensitiveFields[0] = "mutated_snapshot"
	first[0].Operations[0].OperationID = "mutatedSnapshot"
	second := registry.Resources()
	if got, want := second[0].AuditableFields, []string{"status", "title"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("auditable fields = %v, want %v", got, want)
	}
	if got, want := second[0].SensitiveFields, []string{"private_note"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sensitive fields = %v, want %v", got, want)
	}
	if got := second[0].Operations[0].OperationID; got != "createPostmark" {
		t.Fatalf("operation = %q, want createPostmark", got)
	}
	if second[0].Model != reflect.TypeFor[resourcePersistenceModel]() {
		t.Fatalf("normalized model = %v", second[0].Model)
	}
}

func TestResourceDefinitionFailsClosed(t *testing.T) {
	valid := ResourceDefinition{
		Name:            "postmarks",
		Model:           reflect.TypeFor[resourcePersistenceModel](),
		Ownership:       OwnershipOwner,
		Policy:          PolicyRef("owner"),
		AuditableFields: []string{"status"},
		SensitiveFields: []string{"private_note"},
		Operations: []ResourceOperationDefinition{{
			OperationID: "readPostmark",
			ResponseDTO: reflect.TypeFor[resourceResponse](),
		}},
	}
	tests := []struct {
		name   string
		mutate func(*ResourceDefinition)
	}{
		{
			name: "authorization omitted",
			mutate: func(value *ResourceDefinition) {
				value.Policy = ""
			},
		},
		{
			name: "field classification omitted",
			mutate: func(value *ResourceDefinition) {
				value.SensitiveFields = nil
			},
		},
		{
			name: "sensitive field is auditable",
			mutate: func(value *ResourceDefinition) {
				value.AuditableFields = []string{"private_note"}
			},
		},
		{
			name: "database model reused as request DTO",
			mutate: func(value *ResourceDefinition) {
				value.Operations[0].RequestDTO = reflect.TypeFor[resourcePersistenceModel]()
			},
		},
		{
			name: "database model reused as response DTO",
			mutate: func(value *ResourceDefinition) {
				value.Operations[0].ResponseDTO = reflect.TypeFor[*resourcePersistenceModel]()
			},
		},
		{
			name: "operation omitted",
			mutate: func(value *ResourceDefinition) {
				value.Operations = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := cloneResource(valid)
			test.mutate(&definition)
			registry := NewRegistry()
			if err := registry.RegisterResource(definition); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if got := registry.Resources(); len(got) != 0 {
				t.Fatalf("registry retained rejected resource: %#v", got)
			}
		})
	}
}

func TestResourceDefinitionMustMatchRegisteredOperationAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		operation OperationDefinition
	}{
		{
			name: "missing operation",
		},
		{
			name: "public operation",
			operation: OperationDefinition{
				ID:     "readPostmark",
				Method: MethodGet,
				Path:   "/api/v1/postmarks/{id}",
				Public: true,
			},
		},
		{
			name: "wrong permission resource",
			operation: OperationDefinition{
				ID:             "readPostmark",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks/{id}",
				Authentication: "testSession",
				Permission:     "files:read",
				Policy:         PolicyRef("owner"),
			},
		},
		{
			name: "wrong policy",
			operation: OperationDefinition{
				ID:             "readPostmark",
				Method:         MethodGet,
				Path:           "/api/v1/postmarks/{id}",
				Authentication: "testSession",
				Permission:     "postmarks:read",
				Policy:         PolicyRef("custom-scope"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if test.operation.ID != "" {
				if !test.operation.Public {
					if err := registry.RegisterPermission(PermissionDefinition{
						Code:        test.operation.Permission,
						Description: "Read a resource",
					}); err != nil {
						t.Fatal(err)
					}
				}
				if err := registry.RegisterOperation(test.operation); err != nil {
					t.Fatal(err)
				}
			}
			if err := registry.RegisterResource(ResourceDefinition{
				Name:            "postmarks",
				Model:           reflect.TypeFor[resourcePersistenceModel](),
				Ownership:       OwnershipOwner,
				Policy:          PolicyRef("owner"),
				AuditableFields: []string{},
				SensitiveFields: []string{"private_note"},
				Operations: []ResourceOperationDefinition{{
					OperationID: "readPostmark",
					ResponseDTO: reflect.TypeFor[resourceResponse](),
				}},
			}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDuplicateDefinitionsAreRejected(t *testing.T) {
	t.Run("permission", func(t *testing.T) {
		registry := NewRegistry()
		permission := PermissionDefinition{Code: "postmarks:read", Description: "Read postmarks"}
		if err := registry.RegisterPermission(permission); err != nil {
			t.Fatal(err)
		}
		if err := registry.RegisterPermission(permission); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("error = %v, want ErrDuplicate", err)
		}
	})

	t.Run("operation id", func(t *testing.T) {
		registry := NewRegistry()
		first := OperationDefinition{ID: "health", Method: MethodGet, Path: "/api/v1/health/live", Public: true}
		second := OperationDefinition{ID: "health", Method: MethodGet, Path: "/api/v1/health/ready", Public: true}
		if err := registry.RegisterOperation(first); err != nil {
			t.Fatal(err)
		}
		if err := registry.RegisterOperation(second); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("error = %v, want ErrDuplicate", err)
		}
	})

	t.Run("operation route", func(t *testing.T) {
		registry := NewRegistry()
		first := OperationDefinition{ID: "healthLive", Method: MethodGet, Path: "/api/v1/health/live", Public: true}
		second := OperationDefinition{ID: "liveHealth", Method: MethodGet, Path: "/api/v1/health/live", Public: true}
		if err := registry.RegisterOperation(first); err != nil {
			t.Fatal(err)
		}
		if err := registry.RegisterOperation(second); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("error = %v, want ErrDuplicate", err)
		}
	})
}

func TestMigrationBundleIsImmutable(t *testing.T) {
	sources := []MigrationSource{
		{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
		{Dialect: DialectPostgreSQL, Directory: "migrations/postgres"},
		{Dialect: DialectMySQL, Directory: "migrations/mysql"},
	}
	bundle, err := NewMigrationBundle("postmarks", sources...)
	if err != nil {
		t.Fatal(err)
	}

	sources[0].Directory = "changed/by/caller"
	firstSnapshot := bundle.Sources()
	firstSnapshot[0].Directory = "changed/snapshot"
	secondSnapshot := bundle.Sources()

	got := make(map[Dialect]string, len(secondSnapshot))
	for _, source := range secondSnapshot {
		got[source.Dialect] = source.Directory
	}
	want := map[Dialect]string{
		DialectSQLite:     "migrations/sqlite",
		DialectPostgreSQL: "migrations/postgres",
		DialectMySQL:      "migrations/mysql",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration sources = %v, want %v", got, want)
	}

	registry := NewRegistry()
	if err := registry.RegisterMigrationBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMigrationBundle(bundle); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("error = %v, want ErrDuplicate", err)
	}
}

func TestOptionalJobHandlersAndLifecycleHooksAreRegisteredDeterministically(t *testing.T) {
	registry := NewRegistry()
	handler := func(context.Context, json.RawMessage) error { return nil }
	stop := func(context.Context) error { return nil }

	if err := registry.RegisterJobHandler(JobHandlerDefinition{
		Type:    "postmarks.reindex",
		Version: 2,
		Handle:  handler,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterJobHandler(JobHandlerDefinition{
		Type:    "postmarks.reindex",
		Version: 1,
		Handle:  handler,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterLifecycleHook(LifecycleHook{
		Name: "postmarks.close-cache",
		Stop: stop,
	}); err != nil {
		t.Fatal(err)
	}

	jobs := registry.JobHandlers()
	if got, want := []uint{jobs[0].Version, jobs[1].Version}, []uint{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("job order = %v, want %v", got, want)
	}
	if got, want := registry.LifecycleHooks()[0].Name, "postmarks.close-cache"; got != want {
		t.Fatalf("lifecycle hook = %q, want %q", got, want)
	}
	if err := registry.RegisterJobHandler(jobs[0]); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate job error = %v, want ErrDuplicate", err)
	}
	if err := registry.RegisterLifecycleHook(registry.LifecycleHooks()[0]); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate lifecycle error = %v, want ErrDuplicate", err)
	}
}
