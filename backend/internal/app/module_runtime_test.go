package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	frameworkauthz "github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/framework/services"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"gorm.io/gorm"
)

type extensionResponse struct {
	Message string `json:"message"`
}

type contractExtensionRequest struct {
	Name string `json:"name" binding:"required,min=3,max=40"`
}

type contractExtensionResponse struct {
	Name string `json:"name"`
}

type extensionModule struct {
	events *[]string
}

var errTestModuleSchemaBehind = errors.New("test module schema is behind")

type migrationExtensionModule struct {
	current *atomic.Bool
}

type contractExtensionModule struct {
	calls           *atomic.Int32
	invalidResponse bool
}

type requestProtectionExtensionModule struct {
	calls *atomic.Int32
}

type bearerExtensionModule struct{}

type ownerExtensionRecord struct {
	ID      string `gorm:"column:id;primaryKey"`
	OwnerID string `gorm:"column:owner_id;not null"`
	Name    string `gorm:"column:name;not null"`
}

func (ownerExtensionRecord) TableName() string {
	return "owner_extension_items"
}

type ownerExtensionResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ownerExtensionListResponse struct {
	Items []ownerExtensionResponse `json:"items"`
}

type ownerExtensionModule struct {
	db             *gorm.DB
	applyScope     bool
	middlewareLeak bool
}

type replayOwnerExtensionModule struct {
	calls *atomic.Int32
}

type replayOwnerResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type allModeWriteRecord struct {
	ID    string `gorm:"column:id;primaryKey"`
	Value string `gorm:"column:value;not null"`
}

func (allModeWriteRecord) TableName() string {
	return "all_mode_write_records"
}

type allModeOwnGrantModule struct {
	calls *atomic.Int32
}

func (ownerExtensionModule) Name() string {
	return "owner-extension"
}

func (item ownerExtensionModule) Register(
	registry *module.Registry,
) error {
	const (
		authentication = module.AuthenticationRef("ownerBearer")
		operationID    = "listOwnerExtensionItems"
		permission     = "owner-items:read"
		policyRef      = module.PolicyRef("owner")
	)
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        permission,
		Description: "Read owned extension items",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:          authentication,
			Kind:         module.AuthenticationBearer,
			BearerFormat: "opaque-test",
			Description:  "Test owner identity token.",
			Middleware: func(c *gin.Context) {
				token := strings.TrimPrefix(
					c.GetHeader("Authorization"),
					"Bearer ",
				)
				if token != "user-a" && token != "user-b" {
					writeProblem(
						c,
						http.StatusUnauthorized,
						"Authentication required",
						"Supply a valid owner test token.",
					)
					c.Abort()
					return
				}
				requestContext, err := module.ContextWithAuthenticatedActor(
					c.Request.Context(),
					frameworkauthz.NewUserActor(
						token,
						frameworkauthz.Grant{
							Permission: permission,
							Scope:      frameworkauthz.ScopeOwn,
						},
					),
				)
				if err != nil {
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				c.Request = c.Request.WithContext(requestContext)
				c.Next()
			},
		},
	); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             operationID,
		Method:         module.MethodGet,
		Path:           "/api/v1/owner-extension-items",
		Authentication: authentication,
		Permission:     permission,
		Policy:         policyRef,
	}); err != nil {
		return err
	}
	responseType := reflect.TypeFor[ownerExtensionListResponse]()
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "List owned extension items",
		Tag:           "Owner extension",
		SuccessStatus: http.StatusOK,
		ResponseDTO:   responseType,
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterResource(module.ResourceDefinition{
		Name:            "owner-items",
		Model:           reflect.TypeFor[ownerExtensionRecord](),
		Ownership:       module.OwnershipOwner,
		Policy:          policyRef,
		AuditableFields: []string{"name"},
		SensitiveFields: []string{"owner_id"},
		Operations: []module.ResourceOperationDefinition{{
			OperationID: operationID,
			ResponseDTO: responseType,
		}},
	}); err != nil {
		return err
	}
	var middleware []gin.HandlerFunc
	if item.middlewareLeak {
		middleware = []gin.HandlerFunc{func(c *gin.Context) {
			c.JSON(http.StatusOK, ownerExtensionListResponse{
				Items: []ownerExtensionResponse{{
					ID:   "item-b",
					Name: "middleware-secret-b",
				}},
			})
			c.Abort()
		}}
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   operationID,
		Authorization: module.AuthorizationQuery,
		Middleware:    middleware,
		AuthorizedHandler: func(
			c *gin.Context,
			authorization module.RequestAuthorization,
		) {
			runtime, ok := services.RuntimeFromContext(
				c.Request.Context(),
			)
			if !ok {
				c.Status(http.StatusInternalServerError)
				return
			}
			query := runtime.Database.Model(&ownerExtensionRecord{})
			if item.applyScope {
				scoped, scopeErr := authorization.Scope(
					c.Request.Context(),
					query,
				)
				if scopeErr != nil {
					writeProblem(
						c,
						http.StatusForbidden,
						"Permission denied",
						"The item scope could not be applied.",
					)
					return
				}
				query = scoped
			}
			var records []ownerExtensionRecord
			if err := query.Order("id").Find(&records).Error; err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			response := make(
				[]ownerExtensionResponse,
				0,
				len(records),
			)
			for _, record := range records {
				response = append(response, ownerExtensionResponse{
					ID:   record.ID,
					Name: record.Name,
				})
			}
			c.JSON(http.StatusOK, ownerExtensionListResponse{
				Items: response,
			})
		},
	})
}

func (bearerExtensionModule) Name() string {
	return "bearer-extension"
}

func (bearerExtensionModule) Register(
	registry *module.Registry,
) error {
	const authentication = module.AuthenticationRef("testBearer")
	policy, err := frameworkauthz.NewAllScopePolicy()
	if err != nil {
		return err
	}
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        "bearer-extension:read",
		Description: "Read the bearer extension",
	}); err != nil {
		return err
	}
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        "bearer-extension:write",
		Description: "Write the bearer extension",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthorizationPolicy(
		module.AuthorizationPolicy{
			Ref:    "bearer-extension.all",
			Policy: policy,
		},
	); err != nil {
		return err
	}
	if err := registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:          authentication,
			Kind:         module.AuthenticationBearer,
			BearerFormat: "JWT",
			Description:  "Test API bearer token.",
			Middleware: func(c *gin.Context) {
				if c.GetHeader("Authorization") != "Bearer valid-test-token" {
					writeProblem(
						c,
						http.StatusUnauthorized,
						"Authentication required",
						"Supply a valid bearer token.",
					)
					c.Abort()
					return
				}
				requestContext, err := module.ContextWithAuthenticatedActor(
					c.Request.Context(),
					frameworkauthz.NewUserActor(
						"bearer-user",
						frameworkauthz.Grant{
							Permission: "bearer-extension:read",
							Scope:      frameworkauthz.ScopeAll,
						},
						frameworkauthz.Grant{
							Permission: "bearer-extension:write",
							Scope:      frameworkauthz.ScopeAll,
						},
					),
				)
				if err != nil {
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				c.Request = c.Request.WithContext(requestContext)
				c.Next()
			},
		},
	); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             "readBearerExtension",
		Method:         module.MethodGet,
		Path:           "/api/v1/test-bearer",
		Authentication: authentication,
		Permission:     "bearer-extension:read",
		Policy:         "bearer-extension.all",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   "readBearerExtension",
		Summary:       "Read a bearer-authenticated extension",
		Tag:           "Test extension",
		SuccessStatus: http.StatusOK,
		ResponseDTO:   reflect.TypeFor[extensionResponse](),
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   "readBearerExtension",
		Authorization: module.AuthorizationAll,
		AuthorizedHandler: func(
			c *gin.Context,
			authorization module.RequestAuthorization,
		) {
			c.JSON(http.StatusOK, extensionResponse{
				Message: authorization.Actor().ID,
			})
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             "writeBearerExtension",
		Method:         module.MethodPost,
		Path:           "/api/v1/test-bearer",
		Authentication: authentication,
		Permission:     "bearer-extension:write",
		Policy:         "bearer-extension.all",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   "writeBearerExtension",
		Summary:       "Write a bearer-authenticated extension",
		Tag:           "Test extension",
		SuccessStatus: http.StatusNoContent,
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   "writeBearerExtension",
		Authorization: module.AuthorizationAll,
		AuthorizedHandler: func(
			c *gin.Context,
			_ module.RequestAuthorization,
		) {
			c.Status(http.StatusNoContent)
		},
	})
}

func (contractExtensionModule) Name() string {
	return "contract-extension"
}

func (item contractExtensionModule) Register(
	registry *module.Registry,
) error {
	const operationID = "createContractExtension"
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:     operationID,
		Method: module.MethodPost,
		Path:   "/api/v1/test-contract",
		Public: true,
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "Validate an extension request",
		Tag:           "Test extension",
		SuccessStatus: http.StatusCreated,
		RequestDTO:    reflect.TypeFor[contractExtensionRequest](),
		ResponseDTO:   reflect.TypeFor[contractExtensionResponse](),
		ErrorStatuses: []int{
			http.StatusBadRequest,
			http.StatusUnsupportedMediaType,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID: operationID,
		Handler: func(c *gin.Context) {
			input, ok := module.ValidatedRequestDTOFromContext[contractExtensionRequest](c.Request.Context())
			if !ok {
				c.Status(http.StatusInternalServerError)
				return
			}
			item.calls.Add(1)
			if item.invalidResponse {
				c.JSON(http.StatusCreated, gin.H{
					"name":   input.Name,
					"secret": "must-not-leak",
				})
				return
			}
			c.JSON(http.StatusCreated, contractExtensionResponse{
				Name: input.Name,
			})
		},
	})
}

func (requestProtectionExtensionModule) Name() string {
	return "request-protection-extension"
}

func (item requestProtectionExtensionModule) Register(
	registry *module.Registry,
) error {
	const (
		operationID = "createProtectedExtension"
		permission  = "protected-extensions:create"
		policyRef   = module.PolicyRef("protected-extensions.all")
	)
	policy, err := frameworkauthz.NewAllScopePolicy()
	if err != nil {
		return err
	}
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        permission,
		Description: "Create a protected extension result",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthorizationPolicy(
		module.AuthorizationPolicy{Ref: policyRef, Policy: policy},
	); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             operationID,
		Method:         module.MethodPost,
		Path:           "/api/v1/protected-extension",
		Authentication: authenticationCookie,
		Permission:     permission,
		Policy:         policyRef,
		Idempotency:    module.IdempotencyOptional,
		RateLimit: module.RateLimitPolicy{
			Namespace: "protected-extensions.write.actor",
			Subject:   module.RateLimitByActor,
			Limit:     1,
			Window:    time.Hour,
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "Create a protected extension result",
		Tag:           "Test extension",
		SuccessStatus: http.StatusCreated,
		ResponseDTO:   reflect.TypeFor[extensionResponse](),
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   operationID,
		Authorization: module.AuthorizationAll,
		AuthorizedHandler: func(
			c *gin.Context,
			_ module.RequestAuthorization,
		) {
			item.calls.Add(1)
			c.JSON(
				http.StatusCreated,
				extensionResponse{Message: "created"},
			)
		},
	})
}

func (replayOwnerExtensionModule) Name() string {
	return "replay-owner-extension"
}

func (item replayOwnerExtensionModule) Register(
	registry *module.Registry,
) error {
	const (
		authentication = module.AuthenticationRef("replayOwnerBearer")
		operationID    = "updateReplayOwnerItem"
		permission     = "replay-items:update"
		policyRef      = module.PolicyRef("owner")
	)
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        permission,
		Description: "Update an owned replay test item",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:          authentication,
			Kind:         module.AuthenticationBearer,
			BearerFormat: "opaque-test",
			Description:  "Test replay owner identity token.",
			Middleware: func(c *gin.Context) {
				token := strings.TrimPrefix(
					c.GetHeader("Authorization"),
					"Bearer ",
				)
				if token != "user-a" && token != "user-b" {
					writeProblem(
						c,
						http.StatusUnauthorized,
						"Authentication required",
						"Supply a valid replay owner token.",
					)
					c.Abort()
					return
				}
				requestContext, contextErr :=
					module.ContextWithAuthenticatedActor(
						c.Request.Context(),
						frameworkauthz.NewUserActor(
							token,
							frameworkauthz.Grant{
								Permission: permission,
								Scope:      frameworkauthz.ScopeOwn,
							},
						),
					)
				if contextErr != nil {
					c.AbortWithStatus(
						http.StatusInternalServerError,
					)
					return
				}
				c.Request = c.Request.WithContext(requestContext)
				c.Next()
			},
		},
	); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             operationID,
		Method:         module.MethodPost,
		Path:           "/api/v1/replay-owner-items/{id}",
		Authentication: authentication,
		Permission:     permission,
		Policy:         policyRef,
		Idempotency:    module.IdempotencyOptional,
	}); err != nil {
		return err
	}
	responseType := reflect.TypeFor[replayOwnerResponse]()
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "Update an owned replay test item",
		Tag:           "Replay owner extension",
		SuccessStatus: http.StatusOK,
		ResponseDTO:   responseType,
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterResource(module.ResourceDefinition{
		Name:            "replay-items",
		Model:           reflect.TypeFor[ownerExtensionRecord](),
		Ownership:       module.OwnershipOwner,
		Policy:          policyRef,
		AuditableFields: []string{"name"},
		SensitiveFields: []string{"owner_id"},
		Operations: []module.ResourceOperationDefinition{{
			OperationID: operationID,
			ResponseDTO: responseType,
		}},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   operationID,
		Authorization: module.AuthorizationObject,
		ReplayAuthorizer: func(
			ctx context.Context,
			authorization module.RequestAuthorization,
			replay module.IdempotencyReplay,
		) error {
			_, err := loadReplayOwnerRecord(
				ctx,
				authorization,
				replay.PathParameter("id"),
			)
			return err
		},
		AuthorizedHandler: func(
			c *gin.Context,
			authorization module.RequestAuthorization,
		) {
			record, loadErr := loadReplayOwnerRecord(
				c.Request.Context(),
				authorization,
				c.Param("id"),
			)
			if loadErr != nil {
				writeProblem(
					c,
					http.StatusNotFound,
					"Replay item not found",
					"The requested replay item does not exist.",
				)
				return
			}
			item.calls.Add(1)
			c.JSON(http.StatusOK, replayOwnerResponse{
				ID:      record.ID,
				Message: record.Name,
			})
		},
	})
}

func loadReplayOwnerRecord(
	ctx context.Context,
	authorization module.RequestAuthorization,
	id string,
) (ownerExtensionRecord, error) {
	runtime, ok := services.RuntimeFromContext(ctx)
	if !ok {
		return ownerExtensionRecord{}, errors.New(
			"module runtime is unavailable",
		)
	}
	query, err := authorization.Scope(
		ctx,
		runtime.Database.Model(&ownerExtensionRecord{}),
	)
	if err != nil {
		return ownerExtensionRecord{}, err
	}
	var record ownerExtensionRecord
	result := query.Where("id = ?", id).Take(&record)
	return record, result.Error
}

func (allModeOwnGrantModule) Name() string {
	return "all-mode-own-grant"
}

func (item allModeOwnGrantModule) Register(
	registry *module.Registry,
) error {
	const (
		authentication = module.AuthenticationRef("allModeOwnBearer")
		operationID    = "createAllModeRecord"
		permission     = "all-mode-records:create"
	)
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        permission,
		Description: "Create an all-mode test record",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:          authentication,
			Kind:         module.AuthenticationBearer,
			BearerFormat: "opaque-test",
			Description:  "Test own-scope actor for an all-mode route.",
			Middleware: func(c *gin.Context) {
				if c.GetHeader("Authorization") !=
					"Bearer own-scope-user" {
					writeProblem(
						c,
						http.StatusUnauthorized,
						"Authentication required",
						"Supply the all-mode test token.",
					)
					c.Abort()
					return
				}
				requestContext, err :=
					module.ContextWithAuthenticatedActor(
						c.Request.Context(),
						frameworkauthz.NewUserActor(
							"own-scope-user",
							frameworkauthz.Grant{
								Permission: permission,
								Scope:      frameworkauthz.ScopeOwn,
							},
						),
					)
				if err != nil {
					c.AbortWithStatus(
						http.StatusInternalServerError,
					)
					return
				}
				c.Request = c.Request.WithContext(requestContext)
				c.Next()
			},
		},
	); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             operationID,
		Method:         module.MethodPost,
		Path:           "/api/v1/all-mode-records",
		Authentication: authentication,
		Permission:     permission,
		Policy:         "owner",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "Create an all-mode test record",
		Tag:           "All mode",
		SuccessStatus: http.StatusCreated,
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   operationID,
		Authorization: module.AuthorizationAll,
		AuthorizedHandler: func(
			c *gin.Context,
			authorization module.RequestAuthorization,
		) {
			runtime, ok := services.RuntimeFromContext(
				c.Request.Context(),
			)
			if !ok || runtime.Writes == nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			actorID := authorization.Actor().ID
			record := allModeWriteRecord{
				ID:    "must-not-be-created",
				Value: "side-effect",
			}
			err := runtime.Writes.Run(
				c.Request.Context(),
				func(tx *gorm.DB) (frameworkaudit.Event, error) {
					if err := tx.Create(&record).Error; err != nil {
						return frameworkaudit.Event{}, err
					}
					return frameworkaudit.Event{
						ActorID:    &actorID,
						ActorKind:  frameworkaudit.ActorUser,
						Action:     "all-mode:write",
						Resource:   "all-mode-record",
						ResourceID: record.ID,
						Result:     frameworkaudit.ResultSuccess,
						Source:     frameworkaudit.SourceHTTP,
						Summary:    "Wrote an all-mode test record",
						After: frameworkaudit.SanitizedFields{
							"id": record.ID,
						},
					}, nil
				},
			)
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			item.calls.Add(1)
			c.Status(http.StatusCreated)
		},
	})
}

func (migrationExtensionModule) Name() string {
	return "migration-extension"
}

func (item migrationExtensionModule) Register(registry *module.Registry) error {
	bundle, err := module.NewExecutableMigrationBundle(
		"postmarks",
		module.MigrationExecutor{
			Up: func(
				context.Context,
				*sql.DB,
				module.Dialect,
			) error {
				item.current.Store(true)
				return nil
			},
			EnsureCurrent: func(
				context.Context,
				*sql.DB,
				module.Dialect,
			) error {
				if !item.current.Load() {
					return errTestModuleSchemaBehind
				}
				return nil
			},
		},
		module.MigrationSource{
			Dialect:   module.DialectSQLite,
			Directory: "migrations/sqlite",
		},
	)
	if err != nil {
		return err
	}
	return registry.RegisterMigrationBundle(bundle)
}

func (extensionModule) Name() string {
	return "test-extension"
}

func (item extensionModule) Register(registry *module.Registry) error {
	policy, err := frameworkauthz.NewAllScopePolicy()
	if err != nil {
		return err
	}
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        "extensions:read",
		Description: "Read the test extension",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthorizationPolicy(module.AuthorizationPolicy{
		Ref:    "extensions.all",
		Policy: policy,
	}); err != nil {
		return err
	}
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:             "readTestExtension",
		Method:         module.MethodGet,
		Path:           "/api/v1/test-extension",
		Authentication: authenticationCookie,
		Permission:     "extensions:read",
		Policy:         "extensions.all",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   "readTestExtension",
		Summary:       "Read the test extension",
		Tag:           "Test extension",
		SuccessStatus: http.StatusOK,
		ResponseDTO:   reflect.TypeFor[extensionResponse](),
		ErrorStatuses: []int{
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusInternalServerError,
		},
	}); err != nil {
		return err
	}
	if err := registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID:   "readTestExtension",
		Authorization: module.AuthorizationAll,
		Middleware: []gin.HandlerFunc{func(c *gin.Context) {
			c.Header("X-Test-Extension", "middleware")
			c.Next()
		}},
		AuthorizedHandler: func(
			c *gin.Context,
			authorization module.RequestAuthorization,
		) {
			c.Header(
				"X-Test-Authorization-Scope",
				string(authorization.GrantScope()),
			)
			c.JSON(http.StatusOK, extensionResponse{Message: "registered"})
		},
	}); err != nil {
		return err
	}
	if item.events != nil {
		if err := registry.RegisterLifecycleHook(module.LifecycleHook{
			Name: "test-extension",
			Start: func(context.Context) error {
				*item.events = append(*item.events, "start")
				return nil
			},
			Stop: func(context.Context) error {
				*item.events = append(*item.events, "stop")
				return nil
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func TestApplicationModuleOwnsRoutePolicyContractAndLifecycle(t *testing.T) {
	bootstrap := config.Bootstrap{
		AdminEmail:    "admin@example.com",
		AdminPassword: "correct horse battery staple",
	}
	cfg := moduleTestConfig(t, bootstrap)
	db := openMigratedDatabase(t, cfg.Database)
	var events []string
	extension := extensionModule{events: &events}
	if err := BootstrapWithModules(
		t.Context(),
		db,
		bootstrap,
		extension,
	); err != nil {
		t.Fatal(err)
	}
	const regularPassword = "regular user password value"
	createFileUser(
		t,
		db,
		"reader@example.com",
		regularPassword,
		frameworkauthz.ScopeOwn,
	)
	var reader domain.User
	if err := db.Preload("Roles").
		Where("email = ?", "reader@example.com").
		First(&reader).Error; err != nil {
		t.Fatal(err)
	}
	var permission domain.Permission
	if err := db.Where("code = ?", "extensions:read").
		First(&permission).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO role_permissions (role_id, permission_id, scope) VALUES (?, ?, ?)",
		reader.Roles[0].ID,
		permission.ID,
		frameworkauthz.ScopeOwn,
	).Error; err != nil {
		t.Fatal(err)
	}
	server, err := NewWithModules(cfg, db, extension)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/test-extension",
		nil,
	)
	unauthenticatedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthenticated status = %d, body = %s",
			unauthenticatedRecorder.Code,
			unauthenticatedRecorder.Body.String(),
		)
	}
	if got := unauthenticatedRecorder.Header().Get("X-Test-Extension"); got != "" {
		t.Fatalf("module middleware ran before authorization: %q", got)
	}

	readerCookie := loginCookieAs(
		t,
		server,
		"reader@example.com",
		regularPassword,
	)
	ownScopeRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/test-extension",
		nil,
	)
	ownScopeRequest.AddCookie(readerCookie)
	ownScopeRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(ownScopeRecorder, ownScopeRequest)
	if ownScopeRecorder.Code != http.StatusForbidden {
		t.Fatalf(
			"own-scope status = %d, want 403; body = %s",
			ownScopeRecorder.Code,
			ownScopeRecorder.Body.String(),
		)
	}
	if got := ownScopeRecorder.Header().Get("X-Test-Extension"); got != "" {
		t.Fatalf("module middleware ran after rejected scope: %q", got)
	}

	cookie := loginCookie(t, server)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/test-extension", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Test-Extension"); got != "middleware" {
		t.Fatalf("middleware header = %q", got)
	}
	if got := recorder.Header().Get("X-Test-Authorization-Scope"); got != "all" {
		t.Fatalf("authorization scope header = %q", got)
	}
	var response extensionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "registered" {
		t.Fatalf("response = %#v", response)
	}

	document := BuildOpenAPIWithModules(extension)
	operation := document.Paths["/api/v1/test-extension"].Get
	if operation == nil ||
		operation.OperationID != "readTestExtension" ||
		len(operation.Security) != 1 {
		t.Fatalf("extension OpenAPI operation = %#v", operation)
	}

	if err := server.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"start", "stop"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestApplicationModuleReusesDeclaredIdempotencyAndRateLimit(t *testing.T) {
	bootstrap := config.Bootstrap{
		AdminEmail:    "admin@example.com",
		AdminPassword: "correct horse battery staple",
	}
	cfg := moduleTestConfig(t, bootstrap)
	db := openMigratedDatabase(t, cfg.Database)
	var calls atomic.Int32
	extension := requestProtectionExtensionModule{calls: &calls}
	if err := BootstrapWithModules(
		t.Context(),
		db,
		bootstrap,
		extension,
	); err != nil {
		t.Fatal(err)
	}
	server, err := NewWithModules(cfg, db, extension)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, server)

	request := func(key string) *http.Request {
		result := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/protected-extension",
			nil,
		)
		result.AddCookie(cookie)
		addTestCSRF(result)
		if key != "" {
			result.Header.Set(idempotencyHeader, key)
		}
		return result
	}

	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, request("extension-request"))
	if first.Code != http.StatusCreated {
		t.Fatalf(
			"first status = %d, body = %s",
			first.Code,
			first.Body.String(),
		)
	}
	if first.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("first rate-limit headers = %#v", first.Header())
	}

	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, request("extension-request"))
	if replay.Code != http.StatusCreated ||
		replay.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf(
			"replay status/header = %d/%q, body = %s",
			replay.Code,
			replay.Header().Get(idempotencyReplayedHeader),
			replay.Body.String(),
		)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls after replay = %d, want 1", got)
	}

	limited := httptest.NewRecorder()
	server.Handler().ServeHTTP(limited, request(""))
	assertProblemCode(
		t,
		limited,
		http.StatusTooManyRequests,
		"RATE_LIMITED",
	)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls after rate limit = %d, want 1", got)
	}

	document := BuildOpenAPIWithModules(extension)
	operation := document.Paths["/api/v1/protected-extension"].Post
	if operation == nil {
		t.Fatal("extension OpenAPI operation is missing")
	}
	var idempotencyDocumented bool
	for _, parameter := range operation.Parameters {
		if parameter.Name == idempotencyHeader &&
			parameter.In == "header" {
			idempotencyDocumented = true
			break
		}
	}
	if !idempotencyDocumented ||
		operation.Responses["429"] == nil ||
		operation.Responses["503"] == nil {
		t.Fatalf(
			"extension request-protection contract = %#v",
			operation,
		)
	}
}

func TestIdempotencyReplayRechecksCurrentObjectOwnership(
	t *testing.T,
) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	if err := db.Exec(`
		CREATE TABLE owner_extension_items (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	record := ownerExtensionRecord{
		ID:      "item-a",
		OwnerID: "user-a",
		Name:    "owner-a-sensitive-result",
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server, err := NewWithModules(
		cfg,
		db,
		replayOwnerExtensionModule{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := func(actor string) *http.Request {
		result := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/replay-owner-items/item-a",
			nil,
		)
		result.Header.Set("Authorization", "Bearer "+actor)
		result.Header.Set(idempotencyHeader, "owner-replay-key")
		return result
	}

	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, request("user-a"))
	if first.Code != http.StatusOK ||
		!strings.Contains(
			first.Body.String(),
			"owner-a-sensitive-result",
		) {
		t.Fatalf(
			"first response = %d %s",
			first.Code,
			first.Body.String(),
		)
	}
	if err := db.Model(&ownerExtensionRecord{}).
		Where("id = ?", record.ID).
		Update("owner_id", "user-b").
		Error; err != nil {
		t.Fatal(err)
	}

	replayed := httptest.NewRecorder()
	server.Handler().ServeHTTP(replayed, request("user-a"))
	assertProblemCode(
		t,
		replayed,
		http.StatusConflict,
		"IDEMPOTENCY_RESOURCE_MISSING",
	)
	if replayed.Header().Get(idempotencyReplayedHeader) != "" {
		t.Fatalf(
			"unauthorized response marked replayed: %#v",
			replayed.Header(),
		)
	}
	if strings.Contains(
		replayed.Body.String(),
		"owner-a-sensitive-result",
	) {
		t.Fatalf(
			"unauthorized replay exposed cached body: %s",
			replayed.Body.String(),
		)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf(
			"handler calls after unauthorized replay = %d, want 1",
			got,
		)
	}
}

func TestAuthorizationAllRejectsOwnScopeBeforeBusinessWrite(
	t *testing.T,
) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	if err := db.Exec(`
		CREATE TABLE all_mode_write_records (
			id TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server, err := NewWithModules(
		cfg,
		db,
		allModeOwnGrantModule{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/all-mode-records",
		nil,
	)
	request.Header.Set(
		"Authorization",
		"Bearer own-scope-user",
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assertProblemCode(
		t,
		response,
		http.StatusForbidden,
		"RESOURCE_FORBIDDEN",
	)
	if got := calls.Load(); got != 0 {
		t.Fatalf(
			"all-mode handler calls = %d, want 0",
			got,
		)
	}
	var recordCount int64
	if err := db.Model(&allModeWriteRecord{}).
		Count(&recordCount).
		Error; err != nil {
		t.Fatal(err)
	}
	if recordCount != 0 {
		t.Fatalf(
			"all-mode business records = %d, want 0",
			recordCount,
		)
	}
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ?", "all-mode:write").
		Count(&auditCount).
		Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf(
			"all-mode audit records = %d, want 0",
			auditCount,
		)
	}
}

func TestApplicationModuleMigrationsAreExplicitAndReadinessChecked(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	var current atomic.Bool
	extension := migrationExtensionModule{current: &current}

	if _, err := NewWithModules(cfg, db, extension); !errors.Is(
		err,
		errTestModuleSchemaBehind,
	) {
		t.Fatalf("startup error = %v, want module schema error", err)
	}
	if err := BootstrapWithModules(
		t.Context(),
		db,
		config.Bootstrap{},
		extension,
	); !errors.Is(err, errTestModuleSchemaBehind) {
		t.Fatalf("bootstrap error = %v, want module schema error", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(
		t.Context(),
		sqlDB,
		cfg.Database.Driver,
		extension,
	); err != nil {
		t.Fatal(err)
	}
	server, err := NewWithModules(cfg, db, extension)
	if err != nil {
		t.Fatal(err)
	}

	current.Store(false)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/health/ready",
		nil,
	)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"readiness status = %d, want %d; body = %s",
			recorder.Code,
			http.StatusServiceUnavailable,
			recorder.Body.String(),
		)
	}
}

func TestApplicationModuleRequestDTOIsEnforcedAtRuntime(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	var calls atomic.Int32
	server, err := NewWithModules(
		cfg,
		db,
		contractExtensionModule{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"name":"valid","unexpected":true}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "binding validation",
			contentType: "application/json",
			body:        `{"name":"x"}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "wrong media type",
			contentType: "text/plain",
			body:        `{"name":"valid"}`,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "valid",
			contentType: "application/json; charset=utf-8",
			body:        `{"name":"valid"}`,
			wantStatus:  http.StatusCreated,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/test-contract",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			addTestCSRF(request)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body = %s",
					recorder.Code,
					test.wantStatus,
					recorder.Body.String(),
				)
			}
		})
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want only the valid request", got)
	}
}

func TestApplicationModuleResponseDTOIsEnforcedBeforeCommit(
	t *testing.T,
) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	var calls atomic.Int32
	server, err := NewWithModules(
		cfg,
		db,
		contractExtensionModule{
			calls:           &calls,
			invalidResponse: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test-contract",
		strings.NewReader(`{"name":"valid"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), "must-not-leak") {
		t.Fatalf(
			"invalid response escaped contract guard: %s",
			recorder.Body.String(),
		)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestApplicationModuleCanRegisterBearerAuthentication(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	server, err := NewWithModules(cfg, db, bearerExtensionModule{})
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/test-bearer",
		nil,
	)
	unauthenticatedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		unauthenticatedRecorder,
		unauthenticated,
	)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthenticated status = %d, body = %s",
			unauthenticatedRecorder.Code,
			unauthenticatedRecorder.Body.String(),
		)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/test-bearer",
		nil,
	)
	request.Header.Set("Authorization", "Bearer valid-test-token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"bearer status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	document := BuildOpenAPIWithModules(bearerExtensionModule{})
	scheme := document.Components.SecuritySchemes["testBearer"]
	if scheme == nil ||
		scheme.Type != "http" ||
		scheme.Scheme != "bearer" ||
		scheme.BearerFormat != "JWT" {
		t.Fatalf("bearer security scheme = %#v", scheme)
	}
	operation := document.Paths["/api/v1/test-bearer"].Get
	if operation == nil || len(operation.Security) != 1 {
		t.Fatalf("bearer operation security = %#v", operation)
	}
	if _, hasBearerSecurity := operation.Security[0]["testBearer"]; !hasBearerSecurity {
		t.Fatalf("bearer operation security = %#v", operation)
	}

	writeRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test-bearer",
		nil,
	)
	writeRequest.Header.Set(
		"Authorization",
		"Bearer valid-test-token",
	)
	writeRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(writeRecorder, writeRequest)
	if writeRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"bearer write status = %d, body = %s",
			writeRecorder.Code,
			writeRecorder.Body.String(),
		)
	}

	crossOriginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test-bearer",
		nil,
	)
	crossOriginRequest.Header.Set(
		"Authorization",
		"Bearer valid-test-token",
	)
	crossOriginRequest.Header.Set(
		"Origin",
		"https://hostile.example",
	)
	crossOriginRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		crossOriginRecorder,
		crossOriginRequest,
	)
	if crossOriginRecorder.Code != http.StatusForbidden {
		t.Fatalf(
			"cross-origin bearer write status = %d, body = %s",
			crossOriginRecorder.Code,
			crossOriginRecorder.Body.String(),
		)
	}
}

func TestApplicationModuleEnforcesOwnerQueryScopeBeforeSuccess(
	t *testing.T,
) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	if err := db.Exec(`
		CREATE TABLE owner_extension_items (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	for _, record := range []ownerExtensionRecord{
		{ID: "item-a", OwnerID: "user-a", Name: "visible-a"},
		{ID: "item-b", OwnerID: "user-b", Name: "secret-b"},
	} {
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}

	scoped, err := NewWithModules(
		cfg,
		db,
		ownerExtensionModule{db: db, applyScope: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		actor string
		want  string
		deny  string
	}{
		{actor: "user-a", want: "visible-a", deny: "secret-b"},
		{actor: "user-b", want: "secret-b", deny: "visible-a"},
	} {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/owner-extension-items",
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+test.actor)
		recorder := httptest.NewRecorder()
		scoped.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf(
				"%s status = %d, body = %s",
				test.actor,
				recorder.Code,
				recorder.Body.String(),
			)
		}
		if !strings.Contains(recorder.Body.String(), test.want) ||
			strings.Contains(recorder.Body.String(), test.deny) {
			t.Fatalf(
				"%s scoped body = %s",
				test.actor,
				recorder.Body.String(),
			)
		}
	}

	unscoped, err := NewWithModules(
		cfg,
		db,
		ownerExtensionModule{db: db, applyScope: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/owner-extension-items",
		nil,
	)
	request.Header.Set("Authorization", "Bearer user-a")
	recorder := httptest.NewRecorder()
	unscoped.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"unscoped status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), "secret-b") ||
		!strings.Contains(
			recorder.Body.String(),
			"AUTHORIZATION_SCOPE_UNUSED",
		) {
		t.Fatalf("unscoped response leaked data: %s", recorder.Body.String())
	}

	middlewareLeak, err := NewWithModules(
		cfg,
		db,
		ownerExtensionModule{
			db:             db,
			middlewareLeak: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/owner-extension-items",
		nil,
	)
	request.Header.Set("Authorization", "Bearer user-a")
	recorder = httptest.NewRecorder()
	middlewareLeak.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError ||
		strings.Contains(
			recorder.Body.String(),
			"middleware-secret-b",
		) ||
		!strings.Contains(
			recorder.Body.String(),
			"AUTHORIZATION_SCOPE_UNUSED",
		) {
		t.Fatalf(
			"module middleware bypass response = %d %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestApplicationModuleFailsClosedWhenRuntimeRegistrationIsIncomplete(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	incomplete := testModule{
		name: "incomplete-extension",
		register: func(registry *module.Registry) error {
			if err := registry.RegisterPermission(module.PermissionDefinition{
				Code:        "incomplete:read",
				Description: "Read incomplete resources",
			}); err != nil {
				return err
			}
			return registry.RegisterOperation(module.OperationDefinition{
				ID:             "readIncomplete",
				Method:         module.MethodGet,
				Path:           "/api/v1/incomplete",
				Authentication: authenticationCookie,
				Permission:     "incomplete:read",
				Policy:         "incomplete.all",
			})
		},
	}
	_, err := NewWithModules(cfg, db, incomplete)
	if !errors.Is(err, module.ErrInvalid) {
		t.Fatalf("error = %v, want module.ErrInvalid", err)
	}
}

func TestApplicationModuleReturnsRouteConflictInsteadOfPanicking(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	conflicting := testModule{
		name: "conflicting-routes",
		register: func(registry *module.Registry) error {
			for _, definition := range []module.OperationDefinition{
				{
					ID:     "conflictCatchAll",
					Method: module.MethodGet,
					Path:   "/api/v1/conflicts/{rest+}",
					Public: true,
				},
				{
					ID:     "conflictStatic",
					Method: module.MethodGet,
					Path:   "/api/v1/conflicts/static",
					Public: true,
				},
			} {
				if err := registry.RegisterOperation(definition); err != nil {
					return err
				}
				if err := registry.RegisterAPIContract(module.APIContract{
					OperationID:   definition.ID,
					Summary:       "Conflicting route",
					Tag:           "Test extension",
					SuccessStatus: http.StatusNoContent,
				}); err != nil {
					return err
				}
				if err := registry.RegisterHTTPRoute(module.HTTPRoute{
					OperationID: definition.ID,
					Handler: func(c *gin.Context) {
						c.Status(http.StatusNoContent)
					},
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	if _, err := NewWithModules(cfg, db, conflicting); err == nil {
		t.Fatal("NewWithModules accepted conflicting Gin routes")
	}
}

func TestApplicationLifecycleRollsBackStartedHooksInReverseOrder(t *testing.T) {
	startError := errors.New("second hook failed")
	var events []string
	registry := module.NewRegistry()
	if err := registry.RegisterModules(testModule{
		name: "lifecycle",
		register: func(registry *module.Registry) error {
			if err := registry.RegisterLifecycleHook(module.LifecycleHook{
				Name: "alpha",
				Start: func(context.Context) error {
					events = append(events, "alpha.start")
					return nil
				},
				Stop: func(context.Context) error {
					events = append(events, "alpha.stop")
					return nil
				},
			}); err != nil {
				return err
			}
			return registry.RegisterLifecycleHook(module.LifecycleHook{
				Name: "beta",
				Start: func(context.Context) error {
					events = append(events, "beta.start")
					return startError
				},
				Stop: func(context.Context) error {
					events = append(events, "beta.stop")
					return nil
				},
			})
		},
	}); err != nil {
		t.Fatal(err)
	}
	instance := &App{registry: registry}
	if err := instance.Start(t.Context()); !errors.Is(err, startError) {
		t.Fatalf("start error = %v, want %v", err, startError)
	}
	if want := []string{"alpha.start", "beta.start", "alpha.stop"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestApplicationLifecycleRollbackUsesIndependentContext(t *testing.T) {
	startError := errors.New("start canceled")
	var rollbackContextError error
	startContext, cancelStart := context.WithCancel(t.Context())
	registry := module.NewRegistry()
	if err := registry.RegisterModules(testModule{
		name: "lifecycle-cancel",
		register: func(registry *module.Registry) error {
			if err := registry.RegisterLifecycleHook(module.LifecycleHook{
				Name:  "alpha",
				Start: func(context.Context) error { return nil },
				Stop: func(ctx context.Context) error {
					rollbackContextError = ctx.Err()
					return nil
				},
			}); err != nil {
				return err
			}
			return registry.RegisterLifecycleHook(module.LifecycleHook{
				Name: "beta",
				Start: func(context.Context) error {
					cancelStart()
					return startError
				},
			})
		},
	}); err != nil {
		t.Fatal(err)
	}
	instance := &App{registry: registry}
	if err := instance.Start(startContext); !errors.Is(err, startError) {
		t.Fatalf("start error = %v, want %v", err, startError)
	}
	if rollbackContextError != nil {
		t.Fatalf(
			"rollback context error = %v, want independent active context",
			rollbackContextError,
		)
	}
}

func TestApplicationLifecycleReentryFailsWithoutDeadlock(t *testing.T) {
	registry := module.NewRegistry()
	instance := &App{registry: registry}
	var reentryError error
	if err := registry.RegisterModules(testModule{
		name: "lifecycle-reentry",
		register: func(registry *module.Registry) error {
			return registry.RegisterLifecycleHook(module.LifecycleHook{
				Name: "reentrant",
				Start: func(ctx context.Context) error {
					reentryError = instance.Start(ctx)
					return nil
				},
			})
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(reentryError, ErrLifecycleTransition) {
		t.Fatalf(
			"reentry error = %v, want ErrLifecycleTransition",
			reentryError,
		)
	}
}

func TestApplicationLifecycleCachesStartAndShutdownFailures(t *testing.T) {
	t.Run("failed start is not retried", func(t *testing.T) {
		startError := errors.New("start failed")
		var starts atomic.Int32
		registry := module.NewRegistry()
		if err := registry.RegisterModules(testModule{
			name: "lifecycle-start-failure",
			register: func(registry *module.Registry) error {
				return registry.RegisterLifecycleHook(module.LifecycleHook{
					Name: "failing-start",
					Start: func(context.Context) error {
						starts.Add(1)
						return startError
					},
				})
			},
		}); err != nil {
			t.Fatal(err)
		}
		instance := &App{registry: registry}
		first := instance.Start(t.Context())
		second := instance.Start(t.Context())
		if !errors.Is(first, startError) || !errors.Is(second, startError) {
			t.Fatalf("start errors = (%v, %v), want %v", first, second, startError)
		}
		if got := starts.Load(); got != 1 {
			t.Fatalf("start calls = %d, want 1", got)
		}
	})

	t.Run("shutdown returns the original failure", func(t *testing.T) {
		stopError := errors.New("stop failed")
		var stops atomic.Int32
		registry := module.NewRegistry()
		if err := registry.RegisterModules(testModule{
			name: "lifecycle-stop-failure",
			register: func(registry *module.Registry) error {
				return registry.RegisterLifecycleHook(module.LifecycleHook{
					Name:  "failing-stop",
					Start: func(context.Context) error { return nil },
					Stop: func(context.Context) error {
						stops.Add(1)
						return stopError
					},
				})
			},
		}); err != nil {
			t.Fatal(err)
		}
		instance := &App{registry: registry}
		if err := instance.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		first := instance.Shutdown(t.Context())
		second := instance.Shutdown(t.Context())
		if !errors.Is(first, stopError) || !errors.Is(second, stopError) {
			t.Fatalf(
				"shutdown errors = (%v, %v), want %v",
				first,
				second,
				stopError,
			)
		}
		if got := stops.Load(); got != 1 {
			t.Fatalf("stop calls = %d, want 1", got)
		}
	})
}

type testModule struct {
	name     string
	register func(*module.Registry) error
}

func (item testModule) Name() string {
	return item.name
}

func (item testModule) Register(registry *module.Registry) error {
	if item.register == nil {
		return nil
	}
	return item.register(registry)
}

func moduleTestConfig(t *testing.T, bootstrap config.Bootstrap) config.Config {
	t.Helper()
	return config.Config{
		Environment: "test",
		HTTP:        config.HTTP{Address: ":0"},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "module-runtime.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Bootstrap: bootstrap,
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
}
