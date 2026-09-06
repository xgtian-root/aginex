package app

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
)

type accessOperationContractExpectation struct {
	id          string
	method      module.HTTPMethod
	path        string
	permission  string
	idempotent  bool
	status      int
	requestDTO  reflect.Type
	responseDTO reflect.Type
}

func accessOperationContractExpectations() []accessOperationContractExpectation {
	return []accessOperationContractExpectation{
		{
			id: "listPermissions", method: module.MethodGet,
			path: "/api/v1/permissions", permission: "permissions:read",
			status: http.StatusOK, responseDTO: reflect.TypeFor[Page[PermissionResponse]](),
		},
		{
			id: "listRoles", method: module.MethodGet,
			path: "/api/v1/roles", permission: "roles:read",
			status: http.StatusOK, responseDTO: reflect.TypeFor[Page[RoleResponse]](),
		},
		{
			id: "createRole", method: module.MethodPost,
			path: "/api/v1/roles", permission: "roles:create", idempotent: true,
			status: http.StatusCreated, requestDTO: reflect.TypeFor[CreateRoleRequest](), responseDTO: reflect.TypeFor[RoleResponse](),
		},
		{
			id: "getRole", method: module.MethodGet,
			path: "/api/v1/roles/{id}", permission: "roles:read",
			status: http.StatusOK, responseDTO: reflect.TypeFor[RoleResponse](),
		},
		{
			id: "updateRole", method: module.MethodPut,
			path: "/api/v1/roles/{id}", permission: "roles:update", idempotent: true,
			status: http.StatusOK, requestDTO: reflect.TypeFor[UpdateRoleRequest](), responseDTO: reflect.TypeFor[RoleResponse](),
		},
		{
			id: "deleteRole", method: module.MethodDelete,
			path: "/api/v1/roles/{id}", permission: "roles:delete", idempotent: true,
			status: http.StatusNoContent,
		},
		{
			id: "replaceRoleGrants", method: module.MethodPut,
			path: "/api/v1/roles/{id}/grants", permission: "roles:grant", idempotent: true,
			status: http.StatusOK, requestDTO: reflect.TypeFor[ReplaceRoleGrantsRequest](), responseDTO: reflect.TypeFor[RoleResponse](),
		},
		{
			id: "listUsers", method: module.MethodGet,
			path: "/api/v1/users", permission: "users:read",
			status: http.StatusOK, responseDTO: reflect.TypeFor[Page[UserListResponse]](),
		},
		{
			id: "createUser", method: module.MethodPost,
			path: "/api/v1/users", permission: "users:create", idempotent: true,
			status: http.StatusCreated, requestDTO: reflect.TypeFor[CreateUserRequest](), responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "getUser", method: module.MethodGet,
			path: "/api/v1/users/{id}", permission: "users:read",
			status: http.StatusOK, responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "updateUser", method: module.MethodPut,
			path: "/api/v1/users/{id}", permission: "users:update", idempotent: true,
			status: http.StatusOK, requestDTO: reflect.TypeFor[UpdateUserRequest](), responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "deleteUser", method: module.MethodDelete,
			path: "/api/v1/users/{id}", permission: "users:delete", idempotent: true,
			status: http.StatusNoContent,
		},
		{
			id: "replaceUserRoles", method: module.MethodPut,
			path: "/api/v1/users/{id}/roles", permission: "users:assign-roles", idempotent: true,
			status: http.StatusOK, requestDTO: reflect.TypeFor[ReplaceUserRolesRequest](), responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "enableUser", method: module.MethodPost,
			path: "/api/v1/users/{id}/enable", permission: "users:enable", idempotent: true,
			status: http.StatusOK, responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "disableUser", method: module.MethodPost,
			path: "/api/v1/users/{id}/disable", permission: "users:disable", idempotent: true,
			status: http.StatusOK, responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "resetUserPassword", method: module.MethodPut,
			path: "/api/v1/users/{id}/password", permission: "users:reset-password", idempotent: true,
			status: http.StatusNoContent, requestDTO: reflect.TypeFor[ResetUserPasswordRequest](),
		},
		{
			id: "grantUserAdministrator", method: module.MethodPut,
			path: "/api/v1/users/{id}/administrator", permission: "users:grant-administrator", idempotent: true,
			status: http.StatusOK, responseDTO: reflect.TypeFor[UserResponse](),
		},
		{
			id: "revokeUserAdministrator", method: module.MethodDelete,
			path: "/api/v1/users/{id}/administrator", permission: "users:revoke-administrator", idempotent: true,
			status: http.StatusOK, responseDTO: reflect.TypeFor[UserResponse](),
		},
	}
}

func TestAccessOperationDefinitionsAreLocked(t *testing.T) {
	registry, operations, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range accessOperationContractExpectations() {
		t.Run(want.id, func(t *testing.T) {
			operation, ok := operations[want.id]
			if !ok {
				t.Fatalf("operation %q is not registered", want.id)
			}
			if operation.Method != want.method ||
				operation.Path != want.path ||
				operation.Permission != want.permission ||
				operation.Authentication != authenticationCookie ||
				operation.Policy != policySystem ||
				operation.Public {
				t.Fatalf("operation definition = %#v, want %#v", operation, want)
			}
			if operation.Idempotency.Enabled() != want.idempotent {
				t.Fatalf(
					"operation idempotency enabled = %t, want %t",
					operation.Idempotency.Enabled(),
					want.idempotent,
				)
			}
			if mode := builtInAuthorizationMode(want.id); mode != module.AuthorizationAll {
				t.Fatalf("built-in authorization mode = %q, want %q", mode, module.AuthorizationAll)
			}

			contract, ok := registry.APIContract(want.id)
			if !ok {
				t.Fatalf("operation %q has no API contract", want.id)
			}
			if contract.SuccessStatus != want.status ||
				contract.RequestDTO != want.requestDTO ||
				contract.ResponseDTO != want.responseDTO {
				t.Fatalf("API contract = %#v, want %#v", contract, want)
			}
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
				if !accessContractHasStatus(contract.ErrorStatuses, status) {
					t.Fatalf("API contract errors = %v, missing %d", contract.ErrorStatuses, status)
				}
			}
		})
	}
}

func TestAccessOpenAPIContractsAreLocked(t *testing.T) {
	document := BuildOpenAPI()
	for _, want := range accessOperationContractExpectations() {
		t.Run(want.id, func(t *testing.T) {
			operation := operationAt(document, want.method, want.path)
			if operation == nil {
				t.Fatalf("%s %s is missing from OpenAPI", want.method, want.path)
			}
			if operation.OperationID != want.id {
				t.Fatalf("OpenAPI operation ID = %q, want %q", operation.OperationID, want.id)
			}
			if len(operation.Security) != 1 {
				t.Fatalf("security requirements = %#v, want one cookie session requirement", operation.Security)
			}
			if _, ok := operation.Security[0][sessionScheme]; !ok {
				t.Fatalf("security requirements = %#v, missing %q", operation.Security, sessionScheme)
			}

			if hasHeader := headerParameter(operation, idempotencyHeader) != nil; hasHeader != want.idempotent {
				t.Fatalf("Idempotency-Key documented = %t, want %t", hasHeader, want.idempotent)
			}
			csrf := headerParameter(operation, httpx.CSRFHeaderName)
			if wantCSRF := accessOperationRequiresCSRF(want.method); (csrf != nil) != wantCSRF {
				t.Fatalf("CSRF header documented = %t, want %t", csrf != nil, wantCSRF)
			}
			if csrf != nil && !csrf.Required {
				t.Fatalf("CSRF header is not required: %#v", csrf)
			}
			if want.requestDTO == nil {
				if operation.RequestBody != nil {
					t.Fatalf("unexpected request body = %#v", operation.RequestBody)
				}
			} else if operation.RequestBody == nil ||
				operation.RequestBody.Content[jsonMediaType] == nil ||
				operation.RequestBody.Content[jsonMediaType].Schema == nil {
				t.Fatalf("typed JSON request body is missing: %#v", operation.RequestBody)
			}

			successStatus := strconv.Itoa(want.status)
			success := operation.Responses[successStatus]
			if success == nil {
				t.Fatalf("success response %s is missing", successStatus)
			}
			if want.responseDTO != nil &&
				(success.Content[jsonMediaType] == nil ||
					success.Content[jsonMediaType].Schema == nil) {
				t.Fatalf("success response %s has no typed JSON body: %#v", successStatus, success)
			}
			for _, status := range []string{"401", "403", "default"} {
				problem := operation.Responses[status]
				if problem == nil ||
					problem.Content[problemMediaType] == nil ||
					problem.Content[problemMediaType].Schema == nil {
					t.Fatalf("problem response %s is missing or untyped: %#v", status, problem)
				}
			}
		})
	}
}

func TestAccessPermissionRegistryIsLocked(t *testing.T) {
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"permissions:read",
		"roles:create",
		"roles:delete",
		"roles:grant",
		"roles:read",
		"roles:update",
		"users:assign-roles",
		"users:create",
		"users:delete",
		"users:disable",
		"users:enable",
		"users:grant-administrator",
		"users:read",
		"users:reset-password",
		"users:revoke-administrator",
		"users:update",
	}
	got := make([]string, 0, len(want))
	for _, permission := range registry.Permissions() {
		if permission.Code == "permissions:read" ||
			strings.HasPrefix(permission.Code, "roles:") ||
			strings.HasPrefix(permission.Code, "users:") {
			got = append(got, permission.Code)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("access permissions = %v, want %v", got, want)
	}
}

func TestAdministratorBootstrapGrantsExactlyRegistryAtAllScope(t *testing.T) {
	db := openBootstrapDatabase(t)
	if err := Bootstrap(t.Context(), db, config.Bootstrap{}); err != nil {
		t.Fatal(err)
	}
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}

	var administrator domain.Role
	if err := db.Where("name = ?", "Administrator").First(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	type grant struct {
		Code  string
		Scope string
	}
	var grants []grant
	if err := db.Table("role_permissions AS role_grants").
		Select("permissions.code, role_grants.scope").
		Joins("JOIN permissions ON permissions.id = role_grants.permission_id").
		Where("role_grants.role_id = ?", administrator.ID).
		Order("permissions.code ASC").
		Scan(&grants).Error; err != nil {
		t.Fatal(err)
	}

	wantCodes := make([]string, 0, len(registry.Permissions()))
	for _, permission := range registry.Permissions() {
		wantCodes = append(wantCodes, permission.Code)
	}
	gotCodes := make([]string, 0, len(grants))
	for _, current := range grants {
		gotCodes = append(gotCodes, current.Code)
		if current.Scope != "all" {
			t.Errorf("Administrator grant %q scope = %q, want all", current.Code, current.Scope)
		}
	}
	if !reflect.DeepEqual(gotCodes, wantCodes) {
		t.Fatalf("Administrator grants = %v, want registry permissions %v", gotCodes, wantCodes)
	}
}

func accessContractHasStatus(statuses []int, want int) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func accessOperationRequiresCSRF(method module.HTTPMethod) bool {
	switch method {
	case module.MethodPost, module.MethodPut, module.MethodPatch, module.MethodDelete:
		return true
	default:
		return false
	}
}
