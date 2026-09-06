package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"

	"github.com/gin-gonic/gin"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
)

const (
	authenticationCookie module.AuthenticationRef = sessionScheme
	policyOwner          module.PolicyRef         = "owner"
	policySystem         module.PolicyRef         = "system"
)

type staticModule struct {
	name        string
	permissions []module.PermissionDefinition
	operations  []module.OperationDefinition
	resources   []module.ResourceDefinition
	migrations  []module.MigrationBundle
}

func (m staticModule) Name() string {
	return m.name
}

func (m staticModule) Register(registry *module.Registry) error {
	for _, permission := range m.permissions {
		if err := registry.RegisterPermission(permission); err != nil {
			return err
		}
	}
	for _, operation := range m.operations {
		if err := registry.RegisterOperation(operation); err != nil {
			return err
		}
	}
	for _, resource := range m.resources {
		if err := registry.RegisterResource(resource); err != nil {
			return err
		}
	}
	for _, migration := range m.migrations {
		if err := registry.RegisterMigrationBundle(migration); err != nil {
			return err
		}
	}
	return nil
}

// composeRegistry retains the repository's legacy starter composition for
// internal tests and direct internal/app consumers. Public framework
// definitions use composeDefinitionRegistry so their module set is exact.
func composeRegistry(additional ...module.Module) (*module.Registry, map[string]module.OperationDefinition, error) {
	cfg := config.WithDefaults(config.Config{})
	return composeRegistryWithRateLimits(cfg.RateLimit, additional...)
}

func composeRegistryWithRateLimits(
	rateLimits config.RateLimit,
	additional ...module.Module,
) (*module.Registry, map[string]module.OperationDefinition, error) {
	modules := append(coreModules(rateLimits), starterModules(rateLimits)...)
	modules = append(modules, additional...)
	return composeModulesRegistry(rateLimits, modules...)
}

func composeDefinitionRegistry(
	additional ...module.Module,
) (*module.Registry, map[string]module.OperationDefinition, error) {
	cfg := config.WithDefaults(config.Config{})
	return composeDefinitionRegistryWithRateLimits(
		cfg.RateLimit,
		additional...,
	)
}

// ValidateComposition validates the complete fixed-core-plus-explicit-module
// definition and returns every module name included in its fingerprint.
func ValidateComposition(
	additional ...module.Module,
) ([]string, error) {
	registry, _, err := composeDefinitionRegistry(additional...)
	if err != nil {
		return nil, err
	}
	return registry.Modules(), nil
}

func composeDefinitionRegistryWithRateLimits(
	rateLimits config.RateLimit,
	additional ...module.Module,
) (*module.Registry, map[string]module.OperationDefinition, error) {
	modules := append(coreModules(rateLimits), additional...)
	return composeModulesRegistry(rateLimits, modules...)
}

type runtimeRateLimitModule interface {
	withRateLimits(config.RateLimit) module.Module
}

func composeModulesRegistry(
	rateLimits config.RateLimit,
	modules ...module.Module,
) (*module.Registry, map[string]module.OperationDefinition, error) {
	registry := module.NewRegistry()
	configured := make([]module.Module, 0, len(modules))
	for _, candidate := range modules {
		if dynamic, ok := candidate.(runtimeRateLimitModule); ok {
			candidate = dynamic.withRateLimits(rateLimits)
		}
		configured = append(configured, candidate)
	}
	if err := registry.RegisterModules(configured...); err != nil {
		return nil, nil, fmt.Errorf("compose modules: %w", err)
	}
	if err := registerBuiltInRuntimeMetadata(registry); err != nil {
		return nil, nil, fmt.Errorf("register built-in runtime metadata: %w", err)
	}
	operations := make(map[string]module.OperationDefinition)
	for _, operation := range registry.Operations() {
		operations[operation.ID] = operation
	}
	return registry, operations, nil
}

func registerBuiltInRuntimeMetadata(registry *module.Registry) error {
	allPolicy, err := frameworkauthz.NewAllScopePolicy()
	if err != nil {
		return err
	}
	ownerPolicy, err := frameworkauthz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		return err
	}
	for _, definition := range []module.AuthorizationPolicy{
		{Ref: policySystem, Policy: allPolicy},
		{Ref: policyOwner, Policy: ownerPolicy},
	} {
		if err := registry.RegisterAuthorizationPolicy(definition); err != nil {
			return err
		}
	}
	for operationID, contract := range operationContracts() {
		if _, registered := operationByID(registry, operationID); !registered {
			continue
		}
		registered := moduleAPIContract(contract)
		registered.OperationID = operationID
		if err := registry.RegisterAPIContract(registered); err != nil {
			return err
		}
	}
	return nil
}

func coreModules(rateLimits config.RateLimit) []module.Module {
	loginRateLimit := module.RateLimitPolicy{
		Namespace: "auth.login.ip",
		Subject:   module.RateLimitByIP,
		Limit:     rateLimits.LoginLimit,
		Window:    rateLimits.LoginWindow,
	}
	sensitiveRateLimit := module.RateLimitPolicy{
		Namespace: "sensitive.read.user",
		Subject:   module.RateLimitByActor,
		Limit:     rateLimits.SensitiveLimit,
		Window:    rateLimits.SensitiveWindow,
	}
	return []module.Module{
		staticModule{
			name: "storage-settings",
			permissions: []module.PermissionDefinition{
				{Code: "storage-profiles:create", Description: "Create storage profiles"},
				{Code: "storage-profiles:read", Description: "View storage profiles"},
				{Code: "storage-profiles:update", Description: "Update storage profiles"},
				{Code: "storage-profiles:delete", Description: "Delete unused storage profiles"},
				{Code: "storage-profiles:test", Description: "Test storage profile connectivity"},
				{Code: "storage-profiles:activate", Description: "Select the default storage profile"},
				{Code: "storage-profiles:archive", Description: "Archive and restore storage profiles"},
			},
			operations: []module.OperationDefinition{
				protectedOperation("getStorageSettings", module.MethodGet, "/api/v1/storage-settings", "storage-profiles:read", policySystem),
				idempotent(protectedOperation("updateFileUploadPolicy", module.MethodPut, "/api/v1/storage-settings/file-upload-policy", "storage-profiles:update", policySystem)),
				protectedOperation("listStorageProfiles", module.MethodGet, "/api/v1/storage-profiles", "storage-profiles:read", policySystem),
				idempotent(protectedOperation("createStorageProfile", module.MethodPost, "/api/v1/storage-profiles", "storage-profiles:create", policySystem)),
				protectedOperation("getStorageProfile", module.MethodGet, "/api/v1/storage-profiles/{id}", "storage-profiles:read", policySystem),
				idempotent(protectedOperation("updateStorageProfile", module.MethodPut, "/api/v1/storage-profiles/{id}", "storage-profiles:update", policySystem)),
				idempotent(protectedOperation("deleteStorageProfile", module.MethodDelete, "/api/v1/storage-profiles/{id}", "storage-profiles:delete", policySystem)),
				idempotent(rateLimited(protectedOperation("testStorageProfile", module.MethodPost, "/api/v1/storage-profiles/test", "storage-profiles:test", policySystem), sensitiveRateLimit)),
				idempotent(protectedOperation("activateStorageProfile", module.MethodPost, "/api/v1/storage-profiles/{id}/activate", "storage-profiles:activate", policySystem)),
				idempotent(protectedOperation("archiveStorageProfile", module.MethodPost, "/api/v1/storage-profiles/{id}/archive", "storage-profiles:archive", policySystem)),
				idempotent(protectedOperation("restoreStorageProfile", module.MethodPost, "/api/v1/storage-profiles/{id}/restore", "storage-profiles:archive", policySystem)),
			},
			resources: []module.ResourceDefinition{{
				Name: "storage-profiles", Model: reflect.TypeFor[config.StorageProfile](),
				Ownership: module.OwnershipSystem, Policy: policySystem,
				AuditableFields: []string{"name", "provider", "status"},
				SensitiveFields: []string{"access_key_id", "access_key_secret", "account_id", "bucket", "endpoint", "local_root"},
				Operations: []module.ResourceOperationDefinition{
					{OperationID: "getStorageSettings", ResponseDTO: reflect.TypeFor[StorageSettingsResponse]()},
					{OperationID: "updateFileUploadPolicy", RequestDTO: reflect.TypeFor[FileUploadPolicyUpdateRequest](), ResponseDTO: reflect.TypeFor[StorageSettingsResponse]()},
					{OperationID: "listStorageProfiles", ResponseDTO: reflect.TypeFor[Page[StorageProfileResponse]]()},
					{OperationID: "createStorageProfile", RequestDTO: reflect.TypeFor[StorageProfileRequest](), ResponseDTO: reflect.TypeFor[StorageProfileResponse]()},
					{OperationID: "getStorageProfile", ResponseDTO: reflect.TypeFor[StorageProfileResponse]()},
					{OperationID: "updateStorageProfile", RequestDTO: reflect.TypeFor[StorageProfileRequest](), ResponseDTO: reflect.TypeFor[StorageProfileResponse]()},
					{OperationID: "deleteStorageProfile"},
					{OperationID: "testStorageProfile", RequestDTO: reflect.TypeFor[StorageProfileRequest](), ResponseDTO: reflect.TypeFor[StorageProfileTestResponse]()},
					{OperationID: "activateStorageProfile", ResponseDTO: reflect.TypeFor[StorageSettingsResponse]()},
					{OperationID: "archiveStorageProfile", ResponseDTO: reflect.TypeFor[StorageProfileResponse]()},
					{OperationID: "restoreStorageProfile", ResponseDTO: reflect.TypeFor[StorageProfileResponse]()},
				},
			}},
		},
		staticModule{
			name: "access",
			permissions: []module.PermissionDefinition{
				{Code: "audit:read", Description: "View audit history"},
				{Code: "permissions:read", Description: "View permission definitions"},
				{Code: "roles:create", Description: "Create roles"},
				{Code: "roles:delete", Description: "Delete roles"},
				{Code: "roles:grant", Description: "Replace role permission grants"},
				{Code: "roles:read", Description: "View roles and grants"},
				{Code: "roles:update", Description: "Update role metadata"},
				{Code: "users:assign-roles", Description: "Replace user role assignments"},
				{Code: "users:create", Description: "Create users"},
				{Code: "users:delete", Description: "Delete users"},
				{Code: "users:disable", Description: "Disable users"},
				{Code: "users:enable", Description: "Enable users"},
				{Code: "users:grant-administrator", Description: "Grant Administrator access"},
				{Code: "users:read", Description: "View users"},
				{Code: "users:reset-password", Description: "Reset user sign-in passwords"},
				{Code: "users:revoke-administrator", Description: "Revoke Administrator access"},
				{Code: "users:update", Description: "Update user profiles"},
			},
			operations: []module.OperationDefinition{
				rateLimited(protectedOperation("listAuditLogs", module.MethodGet, "/api/v1/audit-logs", "audit:read", policySystem), sensitiveRateLimit),
				rateLimited(protectedOperation("listPermissions", module.MethodGet, "/api/v1/permissions", "permissions:read", policySystem), sensitiveRateLimit),
				rateLimited(protectedOperation("listRoles", module.MethodGet, "/api/v1/roles", "roles:read", policySystem), sensitiveRateLimit),
				idempotent(protectedOperation("createRole", module.MethodPost, "/api/v1/roles", "roles:create", policySystem)),
				protectedOperation("getRole", module.MethodGet, "/api/v1/roles/{id}", "roles:read", policySystem),
				idempotent(protectedOperation("updateRole", module.MethodPut, "/api/v1/roles/{id}", "roles:update", policySystem)),
				idempotent(protectedOperation("deleteRole", module.MethodDelete, "/api/v1/roles/{id}", "roles:delete", policySystem)),
				idempotent(protectedOperation("replaceRoleGrants", module.MethodPut, "/api/v1/roles/{id}/grants", "roles:grant", policySystem)),
				rateLimited(protectedOperation("listUsers", module.MethodGet, "/api/v1/users", "users:read", policySystem), sensitiveRateLimit),
				idempotent(protectedOperation("createUser", module.MethodPost, "/api/v1/users", "users:create", policySystem)),
				protectedOperation("getUser", module.MethodGet, "/api/v1/users/{id}", "users:read", policySystem),
				idempotent(protectedOperation("updateUser", module.MethodPut, "/api/v1/users/{id}", "users:update", policySystem)),
				idempotent(protectedOperation("deleteUser", module.MethodDelete, "/api/v1/users/{id}", "users:delete", policySystem)),
				idempotent(protectedOperation("replaceUserRoles", module.MethodPut, "/api/v1/users/{id}/roles", "users:assign-roles", policySystem)),
				idempotent(protectedOperation("enableUser", module.MethodPost, "/api/v1/users/{id}/enable", "users:enable", policySystem)),
				idempotent(protectedOperation("disableUser", module.MethodPost, "/api/v1/users/{id}/disable", "users:disable", policySystem)),
				idempotent(protectedOperation("resetUserPassword", module.MethodPut, "/api/v1/users/{id}/password", "users:reset-password", policySystem)),
				idempotent(protectedOperation("grantUserAdministrator", module.MethodPut, "/api/v1/users/{id}/administrator", "users:grant-administrator", policySystem)),
				idempotent(protectedOperation("revokeUserAdministrator", module.MethodDelete, "/api/v1/users/{id}/administrator", "users:revoke-administrator", policySystem)),
			},
			resources: []module.ResourceDefinition{
				{
					Name:            "permissions",
					Model:           reflect.TypeFor[domain.Permission](),
					Ownership:       module.OwnershipSystem,
					Policy:          policySystem,
					AuditableFields: []string{"code", "description"},
					SensitiveFields: []string{},
					Operations: []module.ResourceOperationDefinition{{
						OperationID: "listPermissions",
						ResponseDTO: reflect.TypeFor[Page[PermissionResponse]](),
					}},
				},
				{
					Name:            "roles",
					Model:           reflect.TypeFor[domain.Role](),
					Ownership:       module.OwnershipSystem,
					Policy:          policySystem,
					AuditableFields: []string{"description", "name"},
					SensitiveFields: []string{},
					Operations: []module.ResourceOperationDefinition{
						{OperationID: "listRoles", ResponseDTO: reflect.TypeFor[Page[RoleResponse]]()},
						{OperationID: "createRole", RequestDTO: reflect.TypeFor[CreateRoleRequest](), ResponseDTO: reflect.TypeFor[RoleResponse]()},
						{OperationID: "getRole", ResponseDTO: reflect.TypeFor[RoleResponse]()},
						{OperationID: "updateRole", RequestDTO: reflect.TypeFor[UpdateRoleRequest](), ResponseDTO: reflect.TypeFor[RoleResponse]()},
						{OperationID: "deleteRole"},
						{OperationID: "replaceRoleGrants", RequestDTO: reflect.TypeFor[ReplaceRoleGrantsRequest](), ResponseDTO: reflect.TypeFor[RoleResponse]()},
					},
				},
				{
					Name:            "users",
					Model:           reflect.TypeFor[domain.User](),
					Ownership:       module.OwnershipSystem,
					Policy:          policySystem,
					AuditableFields: []string{"display_name", "status"},
					SensitiveFields: []string{"email"},
					Operations: []module.ResourceOperationDefinition{
						{OperationID: "listUsers", ResponseDTO: reflect.TypeFor[Page[UserListResponse]]()},
						{OperationID: "createUser", RequestDTO: reflect.TypeFor[CreateUserRequest](), ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "getUser", ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "updateUser", RequestDTO: reflect.TypeFor[UpdateUserRequest](), ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "deleteUser"},
						{OperationID: "replaceUserRoles", RequestDTO: reflect.TypeFor[ReplaceUserRolesRequest](), ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "enableUser", ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "disableUser", ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "resetUserPassword", RequestDTO: reflect.TypeFor[ResetUserPasswordRequest]()},
						{OperationID: "grantUserAdministrator", ResponseDTO: reflect.TypeFor[UserResponse]()},
						{OperationID: "revokeUserAdministrator", ResponseDTO: reflect.TypeFor[UserResponse]()},
					},
				},
			},
		},
		staticModule{
			name: "authentication",
			permissions: []module.PermissionDefinition{
				{Code: "sessions:delete", Description: "Revoke browser sessions owned by the current user"},
				{Code: "sessions:read", Description: "Read the current session"},
			},
			operations: []module.OperationDefinition{
				{ID: "getCSRFToken", Method: module.MethodGet, Path: "/api/v1/auth/csrf", Public: true},
				rateLimited(module.OperationDefinition{
					ID: "login", Method: module.MethodPost, Path: "/api/v1/auth/login", Public: true,
				}, loginRateLimit),
				protectedOperation("logout", module.MethodPost, "/api/v1/auth/logout", "sessions:delete", policyOwner),
				protectedOperation("revokeAllSessions", module.MethodDelete, "/api/v1/auth/sessions", "sessions:delete", policyOwner),
				protectedOperation("getCurrentUser", module.MethodGet, "/api/v1/auth/me", "sessions:read", policyOwner),
			},
		},
		staticModule{
			name: "health",
			operations: []module.OperationDefinition{
				{ID: "live", Method: module.MethodGet, Path: "/api/v1/health/live", Public: true},
				{ID: "ready", Method: module.MethodGet, Path: "/api/v1/health/ready", Public: true},
			},
		},
		staticModule{
			name: "jobs",
			permissions: []module.PermissionDefinition{
				{Code: "jobs:read", Description: "View durable job metadata"},
				{Code: "jobs:retry", Description: "Retry dead durable jobs"},
			},
			operations: []module.OperationDefinition{
				protectedOperation("listJobs", module.MethodGet, "/api/v1/jobs", "jobs:read", policySystem),
				idempotent(protectedOperation("retryDeadJob", module.MethodPost, "/api/v1/jobs/{id}/retry", "jobs:retry", policySystem)),
			},
		},
	}
}

func starterModules(rateLimits config.RateLimit) []module.Module {
	return []module.Module{
		newFilesModule(rateLimits),
		newStarterExampleModule(),
	}
}

type filesModule struct {
	rateLimits config.RateLimit
}

func FilesModule() module.Module {
	cfg := config.WithDefaults(config.Config{})
	return newFilesModule(cfg.RateLimit)
}

func newFilesModule(rateLimits config.RateLimit) module.Module {
	return filesModule{rateLimits: rateLimits}
}

func (filesModule) Name() string {
	return "files"
}

func (item filesModule) withRateLimits(
	rateLimits config.RateLimit,
) module.Module {
	return newFilesModule(rateLimits)
}

func (item filesModule) Register(registry *module.Registry) error {
	uploadRateLimit := module.RateLimitPolicy{
		Namespace: "files.write.user",
		Subject:   module.RateLimitByActor,
		Limit:     item.rateLimits.UploadLimit,
		Window:    item.rateLimits.UploadWindow,
	}
	sensitiveRateLimit := module.RateLimitPolicy{
		Namespace: "sensitive.read.user",
		Subject:   module.RateLimitByActor,
		Limit:     item.rateLimits.SensitiveLimit,
		Window:    item.rateLimits.SensitiveWindow,
	}
	migration, err := bundledMigrationBundle(bundledFiles)
	if err != nil {
		return err
	}
	return staticModule{
		name: "files",
		permissions: []module.PermissionDefinition{
			{Code: "files:create", Description: "Upload files"},
			{Code: "files:delete", Description: "Delete files"},
			{Code: "files:read", Description: "View files"},
		},
		operations: []module.OperationDefinition{
			protectedOperation("getFileUploadPolicy", module.MethodGet, "/api/v1/files/upload-policy", "files:create", policyOwner),
			protectedOperation("listFiles", module.MethodGet, "/api/v1/files", "files:read", policyOwner),
			idempotent(rateLimited(protectedOperation("createUploadIntent", module.MethodPost, "/api/v1/files/upload-intents", "files:create", policyOwner), uploadRateLimit)),
			protectedOperation("listUploadSessions", module.MethodGet, "/api/v1/files/upload-sessions", "files:create", policyOwner),
			protectedOperation("getUploadSession", module.MethodGet, "/api/v1/files/upload-sessions/{id}", "files:create", policyOwner),
			idempotent(rateLimited(protectedOperation("resumeUploadSession", module.MethodPost, "/api/v1/files/upload-sessions/{id}/resume", "files:create", policyOwner), uploadRateLimit)),
			rateLimited(protectedOperation("signUploadSessionParts", module.MethodPost, "/api/v1/files/upload-sessions/{id}/parts/sign", "files:create", policyOwner), uploadRateLimit),
			idempotent(rateLimited(protectedOperation("ackUploadSessionParts", module.MethodPost, "/api/v1/files/upload-sessions/{id}/parts/ack", "files:create", policyOwner), uploadRateLimit)),
			rateLimited(protectedOperation("localUploadSessionPart", module.MethodPut, "/api/v1/files/upload-sessions/{id}/parts/{number}", "files:create", policyOwner), uploadRateLimit),
			idempotent(rateLimited(protectedOperation("completeUploadSession", module.MethodPost, "/api/v1/files/upload-sessions/{id}/complete", "files:create", policyOwner), uploadRateLimit)),
			idempotent(rateLimited(protectedOperation("cancelUploadSession", module.MethodDelete, "/api/v1/files/upload-sessions/{id}", "files:create", policyOwner), uploadRateLimit)),
			rateLimited(protectedOperation("localUpload", module.MethodPut, "/api/v1/files/local-upload/{key+}", "files:create", policyOwner), uploadRateLimit),
			rateLimited(protectedOperation("localContent", module.MethodGet, "/api/v1/files/local-content/{key+}", "files:read", policyOwner), sensitiveRateLimit),
			idempotent(rateLimited(protectedOperation("confirmUpload", module.MethodPost, "/api/v1/files/{id}/confirm", "files:create", policyOwner), uploadRateLimit)),
			rateLimited(protectedOperation("getFileURL", module.MethodGet, "/api/v1/files/{id}/url", "files:read", policyOwner), sensitiveRateLimit),
			idempotent(rateLimited(protectedOperation("deleteFile", module.MethodDelete, "/api/v1/files/{id}", "files:delete", policyOwner), uploadRateLimit)),
		},
		resources: []module.ResourceDefinition{{
			Name:      "files",
			Model:     reflect.TypeFor[domain.FileObject](),
			Ownership: module.OwnershipOwner,
			Policy:    policyOwner,
			AuditableFields: []string{
				"content_type",
				"height",
				"original_name",
				"sha256",
				"size",
				"status",
				"visibility",
				"width",
			},
			SensitiveFields: []string{
				"bucket",
				"etag",
				"object_key",
				"owner_id",
			},
			Operations: []module.ResourceOperationDefinition{
				{
					OperationID: "getFileUploadPolicy",
					ResponseDTO: reflect.TypeFor[UploadPolicyResponse](),
				},
				{
					OperationID: "listFiles",
					ResponseDTO: reflect.TypeFor[Page[FileResponse]](),
				},
				{
					OperationID: "createUploadIntent",
					RequestDTO:  reflect.TypeFor[UploadIntentRequest](),
					ResponseDTO: reflect.TypeFor[UploadIntentResponse](),
				},
				{OperationID: "listUploadSessions", ResponseDTO: reflect.TypeFor[Page[UploadSessionResponse]]()},
				{OperationID: "getUploadSession", ResponseDTO: reflect.TypeFor[UploadSessionResponse]()},
				{OperationID: "resumeUploadSession", RequestDTO: reflect.TypeFor[ResumeUploadSessionRequest](), ResponseDTO: reflect.TypeFor[UploadSessionResponse]()},
				{OperationID: "signUploadSessionParts", RequestDTO: reflect.TypeFor[SignUploadPartsRequest](), ResponseDTO: reflect.TypeFor[SignUploadPartsResponse]()},
				{OperationID: "ackUploadSessionParts", RequestDTO: reflect.TypeFor[AckUploadPartsRequest](), ResponseDTO: reflect.TypeFor[UploadSessionResponse]()},
				{OperationID: "completeUploadSession", ResponseDTO: reflect.TypeFor[FileResponse]()},
				{OperationID: "cancelUploadSession"},
				{
					OperationID: "confirmUpload",
					ResponseDTO: reflect.TypeFor[FileResponse](),
				},
				{
					OperationID: "getFileURL",
					ResponseDTO: reflect.TypeFor[SignedRequestResponse](),
				},
				{OperationID: "deleteFile"},
			},
		}},
		migrations: []module.MigrationBundle{migration},
	}.Register(registry)
}

type starterExampleModule struct{}

func StarterExampleModule() module.Module {
	return newStarterExampleModule()
}

func newStarterExampleModule() module.Module {
	return starterExampleModule{}
}

func (starterExampleModule) Name() string {
	return "starter-example"
}

func (starterExampleModule) Register(registry *module.Registry) error {
	migration, err := bundledMigrationBundle(bundledStarter)
	if err != nil {
		return err
	}
	return staticModule{
		name: "starter-example",
		permissions: []module.PermissionDefinition{
			{Code: "dashboard:read", Description: "View the dashboard"},
			{Code: "products:create", Description: "Create products"},
			{Code: "products:delete", Description: "Delete products"},
			{Code: "products:read", Description: "View products"},
			{Code: "products:update", Description: "Update products"},
		},
		operations: []module.OperationDefinition{
			protectedOperation("getDashboardSummary", module.MethodGet, "/api/v1/dashboard/summary", "dashboard:read", policySystem),
			protectedOperation("listProducts", module.MethodGet, "/api/v1/products", "products:read", policySystem),
			idempotent(protectedOperation("createProduct", module.MethodPost, "/api/v1/products", "products:create", policySystem)),
			protectedOperation("getProduct", module.MethodGet, "/api/v1/products/{id}", "products:read", policySystem),
			idempotent(protectedOperation("updateProduct", module.MethodPut, "/api/v1/products/{id}", "products:update", policySystem)),
			idempotent(protectedOperation("deleteProduct", module.MethodDelete, "/api/v1/products/{id}", "products:delete", policySystem)),
		},
		resources: []module.ResourceDefinition{{
			Name:            "products",
			Model:           reflect.TypeFor[domain.Product](),
			Ownership:       module.OwnershipSystem,
			Policy:          policySystem,
			AuditableFields: []string{"name", "price_cents", "sku", "status"},
			SensitiveFields: []string{},
			Operations: []module.ResourceOperationDefinition{
				{
					OperationID: "listProducts",
					ResponseDTO: reflect.TypeFor[Page[ProductResponse]](),
				},
				{
					OperationID: "createProduct",
					RequestDTO:  reflect.TypeFor[ProductRequest](),
					ResponseDTO: reflect.TypeFor[ProductResponse](),
				},
				{
					OperationID: "getProduct",
					ResponseDTO: reflect.TypeFor[ProductResponse](),
				},
				{
					OperationID: "updateProduct",
					RequestDTO:  reflect.TypeFor[ProductRequest](),
					ResponseDTO: reflect.TypeFor[ProductResponse](),
				},
				{OperationID: "deleteProduct"},
			},
		}},
		migrations: []module.MigrationBundle{migration},
	}.Register(registry)
}

func protectedOperation(
	id string,
	method module.HTTPMethod,
	path string,
	permission string,
	policy module.PolicyRef,
) module.OperationDefinition {
	return module.OperationDefinition{
		ID:             id,
		Method:         method,
		Path:           path,
		Authentication: authenticationCookie,
		Permission:     permission,
		Policy:         policy,
	}
}

func idempotent(
	operation module.OperationDefinition,
) module.OperationDefinition {
	operation.Idempotency = module.IdempotencyOptional
	return operation
}

func rateLimited(
	operation module.OperationDefinition,
	policy module.RateLimitPolicy,
) module.OperationDefinition {
	operation.RateLimit = policy
	return operation
}

var (
	pathParameterPattern = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_-]*)\}`)
	pathWildcardPattern  = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_-]*)\+\}`)
)

func ginPath(contractPath string) string {
	result := pathWildcardPattern.ReplaceAllString(contractPath, `*$1`)
	return pathParameterPattern.ReplaceAllString(result, `:$1`)
}

func (a *App) bindBuiltInHTTPRoutes() error {
	if err := a.registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:         authenticationCookie,
			Kind:        module.AuthenticationCookie,
			CookieName:  httpx.SessionCookieName,
			Description: "Revocable server-side browser session.",
			Middleware:  a.authenticate(),
		},
	); err != nil {
		return err
	}
	routes := map[string]gin.HandlerFunc{
		"live":                    a.live,
		"ready":                   a.ready,
		"getCSRFToken":            a.csrfToken,
		"login":                   a.login,
		"logout":                  a.logout,
		"revokeAllSessions":       a.revokeAllSessions,
		"getCurrentUser":          a.me,
		"listProducts":            a.listProducts,
		"createProduct":           a.createProduct,
		"getProduct":              a.getProduct,
		"updateProduct":           a.updateProduct,
		"deleteProduct":           a.deleteProduct,
		"listUsers":               a.listUsers,
		"createUser":              a.createUser,
		"getUser":                 a.getUser,
		"updateUser":              a.updateUser,
		"deleteUser":              a.deleteUser,
		"replaceUserRoles":        a.replaceUserRoles,
		"enableUser":              a.enableUser,
		"disableUser":             a.disableUser,
		"resetUserPassword":       a.resetUserPassword,
		"grantUserAdministrator":  a.grantUserAdministrator,
		"revokeUserAdministrator": a.revokeUserAdministrator,
		"listRoles":               a.listRoles,
		"createRole":              a.createRole,
		"getRole":                 a.getRole,
		"updateRole":              a.updateRole,
		"deleteRole":              a.deleteRole,
		"replaceRoleGrants":       a.replaceRoleGrants,
		"listPermissions":         a.listPermissions,
		"listAuditLogs":           a.listAuditLogs,
		"listJobs":                a.listJobs,
		"retryDeadJob":            a.retryDeadJob,
		"getDashboardSummary":     a.dashboardSummary,
		"listFiles":               a.listFiles,
		"getFileUploadPolicy":     a.getFileUploadPolicy,
		"createUploadIntent":      a.createUploadIntent,
		"listUploadSessions":      a.listUploadSessions,
		"getUploadSession":        a.getUploadSession,
		"resumeUploadSession":     a.resumeUploadSession,
		"signUploadSessionParts":  a.signUploadSessionParts,
		"ackUploadSessionParts":   a.ackUploadSessionParts,
		"localUploadSessionPart":  a.localUploadSessionPart,
		"completeUploadSession":   a.completeUploadSession,
		"cancelUploadSession":     a.cancelUploadSession,
		"localUpload":             a.localUpload,
		"localContent":            a.localContent,
		"confirmUpload":           a.confirmUpload,
		"getFileURL":              a.fileURL,
		"deleteFile":              a.deleteFile,
		"getStorageSettings":      a.getStorageSettings,
		"updateFileUploadPolicy":  a.updateFileUploadPolicy,
		"listStorageProfiles":     a.listStorageProfiles,
		"createStorageProfile":    a.createStorageProfile,
		"getStorageProfile":       a.getStorageProfile,
		"updateStorageProfile":    a.updateStorageProfile,
		"deleteStorageProfile":    a.deleteStorageProfile,
		"testStorageProfile":      a.testStorageProfile,
		"activateStorageProfile":  a.activateStorageProfile,
		"archiveStorageProfile":   a.archiveStorageProfile,
		"restoreStorageProfile":   a.restoreStorageProfile,
	}
	for operationID, handler := range routes {
		operation, ok := a.operations[operationID]
		if !ok {
			continue
		}
		route := module.HTTPRoute{OperationID: operationID}
		if operation.Public {
			route.Handler = handler
		} else {
			route.Authorization = builtInAuthorizationMode(operationID)
			route.AuthorizedHandler = func(
				c *gin.Context,
				_ module.RequestAuthorization,
			) {
				handler(c)
			}
			if operation.Idempotency.Enabled() &&
				(route.Authorization == module.AuthorizationObject ||
					route.Authorization == module.AuthorizationQuery) {
				replayOperationID := operationID
				route.ReplayAuthorizer = func(
					ctx context.Context,
					authorization module.RequestAuthorization,
					replay module.IdempotencyReplay,
				) error {
					return a.reauthorizeFileIdempotencyReplay(
						ctx,
						replayOperationID,
						authorization,
						replay,
					)
				}
			}
		}
		if err := a.registry.RegisterHTTPRoute(route); err != nil {
			return err
		}
	}
	return nil
}

func builtInAuthorizationMode(
	operationID string,
) module.AuthorizationMode {
	switch operationID {
	case "logout", "revokeAllSessions", "getCurrentUser":
		return module.AuthorizationActor
	case "listFiles", "listUploadSessions":
		return module.AuthorizationQuery
	case "getFileUploadPolicy", "createUploadIntent", "localUpload", "localContent",
		"confirmUpload", "getFileURL", "deleteFile", "getUploadSession",
		"resumeUploadSession", "signUploadSessionParts", "ackUploadSessionParts",
		"localUploadSessionPart", "completeUploadSession", "cancelUploadSession":
		return module.AuthorizationObject
	default:
		return module.AuthorizationAll
	}
}

func (a *App) mountRegisteredOperations(router *gin.Engine) error {
	if err := a.registry.ValidateRuntime(); err != nil {
		return err
	}
	for _, operation := range a.registry.Operations() {
		route, ok := a.registry.HTTPRoute(operation.ID)
		if !ok {
			return fmt.Errorf("operation %q has no registered HTTP route", operation.ID)
		}
		handlers := make([]gin.HandlerFunc, 0, len(route.Middleware)+5)
		handlers = append(
			handlers,
			a.enforceOperationResponseContract(operation.ID),
		)
		if !operation.Public {
			authenticator, ok := a.registry.AuthenticationScheme(
				operation.Authentication,
			)
			if !ok {
				return fmt.Errorf(
					"operation %q has no registered authenticator",
					operation.ID,
				)
			}
			handlers = append(
				handlers,
				authenticator.Middleware,
				a.requireOperation(
					operation,
					route.Authorization,
				),
			)
		}
		if operation.Idempotency.Enabled() {
			handlers = append(
				handlers,
				a.enforceIdempotency(
					operation.ID,
					operation.Path,
					route.Authorization,
					route.ReplayAuthorizer,
				),
			)
		}
		if operation.RateLimit.Enabled() {
			handlers = append(
				handlers,
				a.operationRateLimiter(operation.RateLimit),
			)
		}
		handlers = append(
			handlers,
			a.validateOperationContract(operation.ID),
		)
		// Request-contract failures are produced before this point. Every
		// response produced by module middleware or handlers, including 4xx and
		// 5xx responses, must consume the declared object/query authorization.
		if !operation.Public {
			handlers = append(
				handlers,
				enforceAuthorizationUsage(route.Authorization),
			)
		}
		handlers = append(handlers, route.Middleware...)
		if operation.Public {
			handlers = append(handlers, route.Handler)
		} else {
			authorizedHandler := route.AuthorizedHandler
			handlers = append(handlers, func(c *gin.Context) {
				decision, ok := module.RequestAuthorizationFromContext(
					c.Request.Context(),
				)
				if !ok {
					writeProblem(
						c,
						http.StatusInternalServerError,
						"Authorization unavailable",
						"The admitted authorization decision is missing.",
					)
					return
				}
				authorizedHandler(c, decision)
			})
		}
		if err := registerGinRoute(
			router,
			string(operation.Method),
			ginPath(operation.Path),
			handlers...,
		); err != nil {
			return fmt.Errorf(
				"mount operation %q: %w",
				operation.ID,
				err,
			)
		}
	}
	return nil
}

func registerGinRoute(
	router *gin.Engine,
	method string,
	path string,
	handlers ...gin.HandlerFunc,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"invalid or conflicting Gin route %s %s: %v",
				method,
				path,
				recovered,
			)
		}
	}()
	router.Handle(method, path, handlers...)
	return nil
}

func (a *App) requireOperation(
	operation module.OperationDefinition,
	mode module.AuthorizationMode,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := module.AuthenticatedActorFromContext(
			c.Request.Context(),
		)
		if !ok {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authentication unavailable",
				"The authenticator did not attach an actor.",
			)
			c.Abort()
			return
		}
		policy, ok := a.registry.AuthorizationPolicy(operation.Policy)
		if !ok {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The operation policy is not registered.",
			)
			c.Abort()
			return
		}
		scope, err := policy.Admit(
			c.Request.Context(),
			actor,
			operation.Permission,
		)
		if err != nil {
			status := http.StatusForbidden
			if errors.Is(err, frameworkauthz.ErrUnauthenticated) {
				status = http.StatusUnauthorized
			}
			writeProblem(
				c,
				status,
				"Permission denied",
				"The registered authorization scope does not allow this operation.",
			)
			c.Abort()
			return
		}
		switch mode {
		case module.AuthorizationAll:
			if scope != frameworkauthz.ScopeAll {
				writeProblem(
					c,
					http.StatusForbidden,
					"Permission denied",
					"This operation requires an all-scope grant.",
				)
				c.Abort()
				return
			}
		case module.AuthorizationActor,
			module.AuthorizationObject,
			module.AuthorizationQuery:
		default:
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The operation authorization mode is invalid.",
			)
			c.Abort()
			return
		}
		decision, err := module.NewRequestAuthorization(
			actor,
			operation.Permission,
			scope,
			policy,
		)
		if err != nil {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The operation policy returned an invalid decision.",
			)
			c.Abort()
			return
		}
		requestContext, err := module.ContextWithRequestAuthorization(
			c.Request.Context(),
			decision,
		)
		if err != nil {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The operation decision could not be attached.",
			)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	}
}
