package module

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/framework/authz"
)

func TestRuntimeModuleRegistersHandlerContractAndPolicy(t *testing.T) {
	policy, err := authz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		t.Fatal(err)
	}
	minimum := float64(1)
	maximum := float64(100)
	module := testModule{name: "postmarks", register: func(registry *Registry) error {
		if err := registry.RegisterPermission(PermissionDefinition{
			Code:        "postmarks:read",
			Description: "Read postmarks",
		}); err != nil {
			return err
		}
		if err := registry.RegisterAuthorizationPolicy(AuthorizationPolicy{
			Ref:    "postmarks.owner",
			Policy: policy,
		}); err != nil {
			return err
		}
		if err := registry.RegisterAuthenticationScheme(AuthenticationScheme{
			Ref:         "testSession",
			Kind:        AuthenticationCookie,
			CookieName:  "test_session",
			Description: "Test browser session.",
			Middleware:  func(c *gin.Context) { c.Next() },
		}); err != nil {
			return err
		}
		if err := registry.RegisterOperation(OperationDefinition{
			ID:             "listPostmarks",
			Method:         MethodGet,
			Path:           "/api/v1/postmarks",
			Authentication: "testSession",
			Permission:     "postmarks:read",
			Policy:         "postmarks.owner",
		}); err != nil {
			return err
		}
		if err := registry.RegisterAPIContract(APIContract{
			OperationID:   "listPostmarks",
			Summary:       "List postmarks",
			Tag:           "Postmarks",
			SuccessStatus: http.StatusOK,
			ResponseDTO:   reflect.TypeFor[[]resourceResponse](),
			Parameters: []ParameterDefinition{{
				Name:        "page",
				In:          ParameterQuery,
				Description: "One-based page number.",
				Type:        ParameterInteger,
				Default:     1,
				Minimum:     &minimum,
				Maximum:     &maximum,
			}},
			ErrorStatuses: []int{
				http.StatusUnauthorized,
				http.StatusForbidden,
				http.StatusInternalServerError,
			},
		}); err != nil {
			return err
		}
		return registry.RegisterHTTPRoute(HTTPRoute{
			OperationID:   "listPostmarks",
			Authorization: AuthorizationQuery,
			Middleware:    []gin.HandlerFunc{func(c *gin.Context) { c.Next() }},
			AuthorizedHandler: func(
				c *gin.Context,
				authorization RequestAuthorization,
			) {
				if authorization.GrantScope() != authz.ScopeAll {
					c.Status(http.StatusForbidden)
					return
				}
				c.Status(http.StatusOK)
			},
		})
	}}

	registry := NewRegistry()
	if err := registry.RegisterModules(module); err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateRuntime(); err != nil {
		t.Fatal(err)
	}

	contract, ok := registry.APIContract("listPostmarks")
	if !ok || contract.Summary != "List postmarks" {
		t.Fatalf("contract = %#v, found = %t", contract, ok)
	}
	contract.Parameters[0].Name = "mutated"
	*contract.Parameters[0].Minimum = 99
	secondContract, _ := registry.APIContract("listPostmarks")
	if secondContract.Parameters[0].Name != "page" ||
		*secondContract.Parameters[0].Minimum != 1 {
		t.Fatalf("contract snapshot was mutable: %#v", secondContract)
	}

	route, ok := registry.HTTPRoute("listPostmarks")
	if !ok || route.AuthorizedHandler == nil || len(route.Middleware) != 1 {
		t.Fatalf("route = %#v, found = %t", route, ok)
	}
	route.Middleware = nil
	secondRoute, _ := registry.HTTPRoute("listPostmarks")
	if len(secondRoute.Middleware) != 1 {
		t.Fatal("route middleware snapshot was mutable")
	}
	if registered, ok := registry.AuthorizationPolicy("postmarks.owner"); !ok || registered != policy {
		t.Fatalf("policy = %#v, found = %t", registered, ok)
	}
}

func TestRuntimeValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Registry) error
	}{
		{
			name: "handler omitted",
			register: func(registry *Registry) error {
				return registerRuntimeFixture(registry, false, true, true)
			},
		},
		{
			name: "contract omitted",
			register: func(registry *Registry) error {
				return registerRuntimeFixture(registry, true, false, true)
			},
		},
		{
			name: "policy omitted",
			register: func(registry *Registry) error {
				return registerRuntimeFixture(registry, true, true, false)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.RegisterModules(testModule{
				name:     "postmarks",
				register: test.register,
			}); err != nil {
				t.Fatal(err)
			}
			if err := registry.ValidateRuntime(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRuntimeValidationRequiresReplayAuthorizerForScopedIdempotency(
	t *testing.T,
) {
	policy, err := authz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	err = registry.RegisterModules(testModule{
		name: "scoped-replay",
		register: func(registry *Registry) error {
			if err := registry.RegisterPermission(
				PermissionDefinition{
					Code:        "items:update",
					Description: "Update an owned item",
				},
			); err != nil {
				return err
			}
			if err := registry.RegisterAuthorizationPolicy(
				AuthorizationPolicy{
					Ref:    "items.owner",
					Policy: policy,
				},
			); err != nil {
				return err
			}
			if err := registry.RegisterAuthenticationScheme(
				AuthenticationScheme{
					Ref:          "testBearer",
					Kind:         AuthenticationBearer,
					BearerFormat: "opaque",
					Description:  "Test bearer.",
					Middleware:   func(c *gin.Context) { c.Next() },
				},
			); err != nil {
				return err
			}
			if err := registry.RegisterOperation(
				OperationDefinition{
					ID:             "updateItem",
					Method:         MethodPost,
					Path:           "/api/v1/items/{id}",
					Authentication: "testBearer",
					Permission:     "items:update",
					Policy:         "items.owner",
					Idempotency:    IdempotencyOptional,
				},
			); err != nil {
				return err
			}
			if err := registry.RegisterAPIContract(APIContract{
				OperationID:   "updateItem",
				Summary:       "Update an item",
				Tag:           "Items",
				SuccessStatus: http.StatusNoContent,
				ErrorStatuses: []int{
					http.StatusUnauthorized,
					http.StatusForbidden,
					http.StatusInternalServerError,
				},
			}); err != nil {
				return err
			}
			return registry.RegisterHTTPRoute(HTTPRoute{
				OperationID:   "updateItem",
				Authorization: AuthorizationObject,
				AuthorizedHandler: func(
					*gin.Context,
					RequestAuthorization,
				) {
				},
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = registry.ValidateRuntime()
	if !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "replay authorizer") {
		t.Fatalf(
			"runtime validation error = %v, want missing replay authorizer",
			err,
		)
	}
}

func TestRuntimeValidationRejectsResourceContractDTODrift(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterModules(testModule{
		name: "postmarks",
		register: func(registry *Registry) error {
			if err := registerRuntimeFixture(registry, true, true, true); err != nil {
				return err
			}
			return registry.RegisterResource(ResourceDefinition{
				Name:            "postmarks",
				Model:           reflect.TypeFor[resourcePersistenceModel](),
				Ownership:       OwnershipCustom,
				Policy:          "postmarks.owner",
				AuditableFields: []string{},
				SensitiveFields: []string{},
				Operations: []ResourceOperationDefinition{{
					OperationID: "listPostmarks",
					ResponseDTO: reflect.TypeFor[resourceResponse](),
				}},
			})
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateRuntime(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestRuntimeCrossReferencesAreValidatedAtomically(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Registry) error
	}{
		{
			name: "route references unknown operation",
			register: func(registry *Registry) error {
				return registry.RegisterHTTPRoute(HTTPRoute{
					OperationID: "missingOperation",
					Handler:     func(*gin.Context) {},
				})
			},
		},
		{
			name: "contract references unknown operation",
			register: func(registry *Registry) error {
				return registry.RegisterAPIContract(APIContract{
					OperationID:   "missingOperation",
					Summary:       "Missing",
					Tag:           "Missing",
					SuccessStatus: http.StatusOK,
				})
			},
		},
		{
			name: "contract path parameter is absent",
			register: func(registry *Registry) error {
				if err := registry.RegisterOperation(OperationDefinition{
					ID:     "readPostmark",
					Method: MethodGet,
					Path:   "/api/v1/postmarks/{id}",
					Public: true,
				}); err != nil {
					return err
				}
				return registry.RegisterAPIContract(APIContract{
					OperationID:   "readPostmark",
					Summary:       "Read postmark",
					Tag:           "Postmarks",
					SuccessStatus: http.StatusOK,
					Parameters: []ParameterDefinition{{
						Name:        "slug",
						In:          ParameterPath,
						Description: "Postmark slug.",
						Required:    true,
						Type:        ParameterString,
					}},
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			err := registry.RegisterModules(testModule{
				name:     "postmarks",
				register: test.register,
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if got := registry.Modules(); len(got) != 0 {
				t.Fatalf("failed registration mutated registry: %v", got)
			}
		})
	}
}

func TestRuntimeDefinitionsRejectUnsafeShapes(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterHTTPRoute(HTTPRoute{
		OperationID: "unsafeRoute",
		Handler:     func(*gin.Context) {},
		Middleware:  []gin.HandlerFunc{nil},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil middleware error = %v, want ErrInvalid", err)
	}

	if err := registry.RegisterAPIContract(APIContract{
		OperationID:   "unsafeContract",
		Summary:       "Unsafe contract",
		Tag:           "Unsafe",
		SuccessStatus: http.StatusNoContent,
		ResponseDTO:   reflect.TypeFor[resourceResponse](),
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("204 response error = %v, want ErrInvalid", err)
	}

	policy, err := authz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterAuthorizationPolicy(AuthorizationPolicy{
		Ref: "postmarks.owner", Policy: policy,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterAuthorizationPolicy(AuthorizationPolicy{
		Ref: "postmarks.owner", Policy: policy,
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate policy error = %v, want ErrDuplicate", err)
	}
}

func TestReadinessChecksAreValidatedAndOrdered(t *testing.T) {
	registry := NewRegistry()
	for _, check := range []ReadinessCheck{
		{
			Name:        "zeta.external",
			Requirement: ReadinessOptional,
			Timeout:     time.Second,
			Check:       func(context.Context) error { return nil },
		},
		{
			Name:        "alpha.external",
			Requirement: ReadinessRequired,
			Timeout:     2 * time.Second,
			Check:       func(context.Context) error { return nil },
		},
	} {
		if err := registry.RegisterReadinessCheck(check); err != nil {
			t.Fatal(err)
		}
	}
	checks := registry.ReadinessChecks()
	if len(checks) != 2 ||
		checks[0].Name != "alpha.external" ||
		checks[1].Name != "zeta.external" {
		t.Fatalf("readiness order = %#v", checks)
	}
	if err := registry.RegisterReadinessCheck(ReadinessCheck{
		Name:        "alpha.external",
		Requirement: ReadinessRequired,
		Timeout:     time.Second,
		Check:       func(context.Context) error { return nil },
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate readiness error = %v", err)
	}
	for _, invalid := range []ReadinessCheck{
		{
			Name:        "missing-timeout",
			Requirement: ReadinessRequired,
			Check:       func(context.Context) error { return nil },
		},
		{
			Name:        "missing-handler",
			Requirement: ReadinessRequired,
			Timeout:     time.Second,
		},
		{
			Name:    "missing-requirement",
			Timeout: time.Second,
			Check:   func(context.Context) error { return nil },
		},
	} {
		if err := NewRegistry().RegisterReadinessCheck(invalid); !errors.Is(
			err,
			ErrInvalid,
		) {
			t.Fatalf("invalid readiness error = %v", err)
		}
	}
}

func registerRuntimeFixture(
	registry *Registry,
	withHandler bool,
	withContract bool,
	withPolicy bool,
) error {
	if err := registry.RegisterPermission(PermissionDefinition{
		Code:        "postmarks:read",
		Description: "Read postmarks",
	}); err != nil {
		return err
	}
	if err := registry.RegisterOperation(OperationDefinition{
		ID:             "listPostmarks",
		Method:         MethodGet,
		Path:           "/api/v1/postmarks",
		Authentication: "testSession",
		Permission:     "postmarks:read",
		Policy:         "postmarks.owner",
	}); err != nil {
		return err
	}
	if withHandler {
		if err := registry.RegisterHTTPRoute(HTTPRoute{
			OperationID:   "listPostmarks",
			Authorization: AuthorizationQuery,
			AuthorizedHandler: func(
				*gin.Context,
				RequestAuthorization,
			) {
			},
		}); err != nil {
			return err
		}
	}
	if withContract {
		if err := registry.RegisterAPIContract(APIContract{
			OperationID:   "listPostmarks",
			Summary:       "List postmarks",
			Tag:           "Postmarks",
			SuccessStatus: http.StatusOK,
			ErrorStatuses: []int{
				http.StatusUnauthorized,
				http.StatusForbidden,
			},
		}); err != nil {
			return err
		}
	}
	if withPolicy {
		policy, err := authz.NewOwnerColumnPolicy("owner_id")
		if err != nil {
			return err
		}
		if err := registry.RegisterAuthorizationPolicy(AuthorizationPolicy{
			Ref: "postmarks.owner", Policy: policy,
		}); err != nil {
			return err
		}
	}
	if err := registry.RegisterAuthenticationScheme(AuthenticationScheme{
		Ref:         "testSession",
		Kind:        AuthenticationCookie,
		CookieName:  "test_session",
		Description: "Test browser session.",
		Middleware:  func(c *gin.Context) { c.Next() },
	}); err != nil {
		return err
	}
	return nil
}
