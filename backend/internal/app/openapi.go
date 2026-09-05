package app

import (
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/framework/jobs"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/internal/config"
)

const (
	jsonMediaType    = "application/json"
	problemMediaType = "application/problem+json"
	sessionScheme    = "cookieSession"
)

type operationContract struct {
	summary       string
	tag           string
	status        int
	request       reflect.Type
	requestMedia  string
	response      reflect.Type
	responseMedia string
	parameters    []*huma.Param
	errors        []int
}

// BuildOpenAPI composes the HTTP contract without opening a database,
// configuring object storage, applying migrations, or bootstrapping data.
func BuildOpenAPI() *huma.OpenAPI {
	return BuildOpenAPIWithModules()
}

// BuildOpenAPIWithModules generates the same runtime contract for additional
// compiled-in modules without opening a database or invoking lifecycle hooks.
func BuildOpenAPIWithModules(applicationModules ...module.Module) *huma.OpenAPI {
	return buildOpenAPI(false, applicationModules...)
}

// BuildCompositionOpenAPI generates the contract for the fixed framework core
// plus exactly the supplied modules. It is the public Definition boundary and
// deliberately excludes this repository's starter/example modules by default.
func BuildCompositionOpenAPI(
	applicationModules ...module.Module,
) *huma.OpenAPI {
	return buildOpenAPI(true, applicationModules...)
}

func buildOpenAPI(
	exactComposition bool,
	applicationModules ...module.Module,
) *huma.OpenAPI {
	var (
		registry   *module.Registry
		operations map[string]module.OperationDefinition
		err        error
	)
	if exactComposition {
		registry, operations, err = composeDefinitionRegistry(
			applicationModules...,
		)
	} else {
		registry, operations, err = composeRegistry(applicationModules...)
	}
	if err != nil {
		panic(err)
	}
	instance := &App{
		cfg: config.WithDefaults(config.Config{
			Environment: "contract",
			Session: config.Session{
				CookieName: httpx.SessionCookieName,
				CSRFCookie: httpx.CSRFCookieName,
				CSRFHeader: httpx.CSRFHeaderName,
			},
			WebOrigin: "http://localhost:3000",
		}),
		registry:   registry,
		operations: operations,
	}
	router, err := instance.routes()
	if err != nil {
		panic(err)
	}
	instance.http = router
	return instance.openapi
}

func documentGinOperations(
	openapi *huma.OpenAPI,
	registry *module.Registry,
) {
	if openapi.Components.SecuritySchemes == nil {
		openapi.Components.SecuritySchemes = make(map[string]*huma.SecurityScheme)
	}
	for _, definition := range registry.AuthenticationSchemes() {
		scheme := &huma.SecurityScheme{
			Description: definition.Description,
		}
		switch definition.Kind {
		case module.AuthenticationCookie:
			scheme.Type = "apiKey"
			scheme.Name = definition.CookieName
			scheme.In = "cookie"
		case module.AuthenticationBearer:
			scheme.Type = "http"
			scheme.Scheme = "bearer"
			scheme.BearerFormat = definition.BearerFormat
		}
		openapi.Components.SecuritySchemes[string(definition.Ref)] = scheme
	}

	for _, definition := range registry.Operations() {
		registered, ok := registry.APIContract(definition.ID)
		if !ok {
			panic("missing API contract for operation " + definition.ID)
		}
		registered = effectiveAPIContract(registered, definition)
		contract := operationContractFromModule(registered)
		if operationRequiresCSRF(registry, definition) {
			contract.parameters = requireCSRFParameter(
				contract.parameters,
			)
			contract.errors = appendStatus(
				contract.errors,
				http.StatusForbidden,
			)
		}
		if definition.Idempotency.Enabled() {
			contract.parameters = append(contract.parameters, idempotencyParameter())
		}
		operation := &huma.Operation{
			Method:      string(definition.Method),
			Path:        definition.Path,
			OperationID: definition.ID,
			Summary:     contract.summary,
			Tags:        []string{contract.tag},
			Parameters:  operationParameters(definition.Path, contract.parameters),
			Responses:   operationResponses(openapi, contract),
		}
		if definition.Public {
			operation.Security = []map[string][]string{}
		} else {
			operation.Security = []map[string][]string{{
				string(definition.Authentication): {},
			}}
		}
		if contract.request != nil {
			mediaType := contract.requestMedia
			if mediaType == "" {
				mediaType = jsonMediaType
			}
			operation.RequestBody = &huma.RequestBody{
				Required: true,
				Content: map[string]*huma.MediaType{
					mediaType: {Schema: contractSchema(openapi, contract.request, definition.ID+"Request")},
				},
			}
		}
		openapi.AddOperation(operation)
	}
}

func operationContracts() map[string]operationContract {
	pageParams := func() []*huma.Param {
		return []*huma.Param{pageParameter(), pageSizeParameter()}
	}
	uploadSessionParams := func() []*huma.Param {
		return []*huma.Param{
			pageParameter(),
			pageSizeParameter(),
			queryEnumParameter("state", "Upload session state filter.", "incomplete"),
		}
	}
	protectedReadErrors := []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusInternalServerError,
	}
	ifMatch := func() []*huma.Param {
		return []*huma.Param{{
			Name: "If-Match", In: "header", Required: true,
			Description: "Quoted storage configuration revision returned in ETag.",
			Schema:      &huma.Schema{Type: "string", Pattern: `^"[1-9][0-9]*"$`},
		}}
	}
	return map[string]operationContract{
		"getStorageSettings": {
			summary: "Get storage profile runtime and restart state", tag: "Storage", status: http.StatusOK,
			response: reflect.TypeFor[StorageSettingsResponse](), errors: protectedReadErrors,
		},
		"updateFileUploadPolicy": {
			summary: "Update the restart-applied file upload policy", tag: "Storage", status: http.StatusOK,
			request: reflect.TypeFor[FileUploadPolicyUpdateRequest](), response: reflect.TypeFor[StorageSettingsResponse](), parameters: ifMatch(),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"listStorageProfiles": {
			summary: "List storage profiles", tag: "Storage", status: http.StatusOK,
			response: reflect.TypeFor[Page[StorageProfileResponse]](), parameters: pageParams(), errors: protectedReadErrors,
		},
		"createStorageProfile": {
			summary: "Create a storage profile", tag: "Storage", status: http.StatusCreated,
			request: reflect.TypeFor[StorageProfileRequest](), response: reflect.TypeFor[StorageProfileResponse](), parameters: ifMatch(),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"getStorageProfile": {
			summary: "Get a storage profile", tag: "Storage", status: http.StatusOK,
			response: reflect.TypeFor[StorageProfileResponse](), errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"updateStorageProfile": {
			summary: "Update a storage profile", tag: "Storage", status: http.StatusOK,
			request: reflect.TypeFor[StorageProfileRequest](), response: reflect.TypeFor[StorageProfileResponse](), parameters: ifMatch(),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"deleteStorageProfile": {
			summary: "Delete an unused storage profile", tag: "Storage", status: http.StatusNoContent, parameters: ifMatch(),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"testStorageProfile": {
			summary: "Test a candidate storage profile", tag: "Storage", status: http.StatusOK,
			request: reflect.TypeFor[StorageProfileRequest](), response: reflect.TypeFor[StorageProfileTestResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"activateStorageProfile": {
			summary: "Select the storage profile used after restart", tag: "Storage", status: http.StatusOK, parameters: ifMatch(),
			response: reflect.TypeFor[StorageSettingsResponse](), errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"archiveStorageProfile": {
			summary: "Archive a storage profile", tag: "Storage", status: http.StatusOK, parameters: ifMatch(),
			response: reflect.TypeFor[StorageProfileResponse](), errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"restoreStorageProfile": {
			summary: "Restore an archived storage profile", tag: "Storage", status: http.StatusOK, parameters: ifMatch(),
			response: reflect.TypeFor[StorageProfileResponse](), errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"live": {
			summary: "Check whether the API process is alive", tag: "Health", status: http.StatusOK,
			response: reflect.TypeFor[HealthResponse](),
			errors:   []int{http.StatusInternalServerError},
		},
		"ready": {
			summary: "Check whether the API is ready to serve traffic", tag: "Health", status: http.StatusOK,
			response: reflect.TypeFor[HealthResponse](),
			errors:   []int{http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		"getCSRFToken": {
			summary: "Issue a double-submit CSRF token", tag: "Authentication", status: http.StatusOK,
			response: reflect.TypeFor[CSRFTokenResponse](),
			errors:   []int{http.StatusInternalServerError},
		},
		"login": {
			summary: "Sign in with a local account", tag: "Authentication", status: http.StatusOK,
			request: reflect.TypeFor[LoginRequest](), response: reflect.TypeFor[UserResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"logout": {
			summary: "Revoke the current session", tag: "Authentication", status: http.StatusNoContent,
			errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		},
		"revokeAllSessions": {
			summary: "Revoke all browser sessions owned by the current user", tag: "Authentication", status: http.StatusNoContent,
			errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		},
		"getCurrentUser": {
			summary: "Get the current principal and permissions", tag: "Authentication", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](), errors: protectedReadErrors,
		},
		"getDashboardSummary": {
			summary: "Get workspace summary metrics", tag: "Dashboard", status: http.StatusOK,
			response: reflect.TypeFor[DashboardSummaryResponse](), errors: protectedReadErrors,
		},
		"listProducts": {
			summary: "List and search products", tag: "Products", status: http.StatusOK,
			response: reflect.TypeFor[Page[ProductResponse]](),
			parameters: append(pageParams(),
				queryStringParameter("search", "Search product name or SKU."),
				queryEnumParameter("status", "Filter by product status.", "draft", "active", "archived"),
			),
			errors: protectedReadErrors,
		},
		"createProduct": {
			summary: "Create a product", tag: "Products", status: http.StatusCreated,
			request: reflect.TypeFor[ProductRequest](), response: reflect.TypeFor[ProductResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"getProduct": {
			summary: "Get a product", tag: "Products", status: http.StatusOK,
			response: reflect.TypeFor[ProductResponse](),
			errors:   []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"updateProduct": {
			summary: "Update a product", tag: "Products", status: http.StatusOK,
			request: reflect.TypeFor[ProductRequest](), response: reflect.TypeFor[ProductResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"deleteProduct": {
			summary: "Delete a product", tag: "Products", status: http.StatusNoContent,
			errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"listUsers": {
			summary: "List and search users", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[Page[UserListResponse]](),
			parameters: append(pageParams(),
				queryStringParameter("search", "Search user email or display name."),
				queryEnumParameter("status", "Filter by user status.", "active", "disabled"),
				queryStringParameter("roleId", "Filter by assigned role identifier."),
			),
			errors: protectedReadErrors,
		},
		"createUser": {
			summary: "Create a local user", tag: "Access", status: http.StatusCreated,
			request: reflect.TypeFor[CreateUserRequest](), response: reflect.TypeFor[UserResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"getUser": {
			summary: "Get a user", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"updateUser": {
			summary: "Update a user profile", tag: "Access", status: http.StatusOK,
			request: reflect.TypeFor[UpdateUserRequest](), response: reflect.TypeFor[UserResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"deleteUser": {
			summary: "Delete a user", tag: "Access", status: http.StatusNoContent,
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"replaceUserRoles": {
			summary: "Replace a user's custom role assignments", tag: "Access", status: http.StatusOK,
			request: reflect.TypeFor[ReplaceUserRolesRequest](), response: reflect.TypeFor[UserResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"enableUser": {
			summary: "Enable a user", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"disableUser": {
			summary: "Disable a user and revoke their sessions", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"resetUserPassword": {
			summary: "Reset a user's local password and revoke their sessions", tag: "Access", status: http.StatusNoContent,
			request: reflect.TypeFor[ResetUserPasswordRequest](),
			errors:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"grantUserAdministrator": {
			summary: "Grant Administrator access to a login-capable user", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"revokeUserAdministrator": {
			summary: "Revoke Administrator access from a user", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[UserResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"listRoles": {
			summary: "List and search roles and grants", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[Page[RoleResponse]](),
			parameters: append(pageParams(),
				queryStringParameter("search", "Search role name or description."),
			),
			errors: protectedReadErrors,
		},
		"createRole": {
			summary: "Create a custom role", tag: "Access", status: http.StatusCreated,
			request: reflect.TypeFor[CreateRoleRequest](), response: reflect.TypeFor[RoleResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"getRole": {
			summary: "Get a role and its grants", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[RoleResponse](),
			errors:   []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"updateRole": {
			summary: "Update custom role metadata", tag: "Access", status: http.StatusOK,
			request: reflect.TypeFor[UpdateRoleRequest](), response: reflect.TypeFor[RoleResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"deleteRole": {
			summary: "Delete an unused custom role", tag: "Access", status: http.StatusNoContent,
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		"replaceRoleGrants": {
			summary: "Replace a custom role's permission grants", tag: "Access", status: http.StatusOK,
			request: reflect.TypeFor[ReplaceRoleGrantsRequest](), response: reflect.TypeFor[RoleResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"listPermissions": {
			summary: "List permission definitions", tag: "Access", status: http.StatusOK,
			response: reflect.TypeFor[Page[PermissionResponse]](),
			parameters: append(pageParams(),
				queryStringParameter("search", "Search permission code or description."),
			),
			errors: protectedReadErrors,
		},
		"listAuditLogs": {
			summary: "List audit events", tag: "Audit", status: http.StatusOK,
			response: reflect.TypeFor[Page[AuditLogResponse]](), parameters: pageParams(), errors: protectedReadErrors,
		},
		"listJobs": {
			summary: "List durable job metadata for operators", tag: "Jobs", status: http.StatusOK,
			response: reflect.TypeFor[Page[JobResponse]](),
			parameters: append(
				pageParams(),
				jobStateParameter(),
				jobTypeParameter(),
			),
			errors: []int{
				http.StatusBadRequest,
				http.StatusUnauthorized,
				http.StatusForbidden,
				http.StatusServiceUnavailable,
				http.StatusInternalServerError,
			},
		},
		"retryDeadJob": {
			summary: "Move a dead durable job back to pending", tag: "Jobs", status: http.StatusAccepted,
			response: reflect.TypeFor[JobRetryResponse](),
			errors: []int{
				http.StatusBadRequest,
				http.StatusUnauthorized,
				http.StatusForbidden,
				http.StatusNotFound,
				http.StatusConflict,
				http.StatusServiceUnavailable,
				http.StatusInternalServerError,
			},
		},
		"listFiles": {
			summary: "List file metadata in the caller's authorized scope", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[Page[FileResponse]](), parameters: pageParams(), errors: protectedReadErrors,
		},
		"getFileUploadPolicy": {
			summary: "Get the effective file upload policy", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[UploadPolicyResponse](), errors: protectedReadErrors,
		},
		"createUploadIntent": {
			summary: "Create a single or resumable upload intent", tag: "Files", status: http.StatusCreated,
			request: reflect.TypeFor[UploadIntentRequest](), response: reflect.TypeFor[UploadIntentResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable},
		},
		"listUploadSessions": {
			summary: "List resumable upload sessions in the caller's authorized scope", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[Page[UploadSessionResponse]](), parameters: uploadSessionParams(),
			errors: appendUniqueStatuses(protectedReadErrors, http.StatusBadRequest),
		},
		"getUploadSession": {
			summary: "Get resumable upload progress", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[UploadSessionResponse](), errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"resumeUploadSession": {
			summary: "Reconcile and resume an interrupted upload", tag: "Files", status: http.StatusOK,
			request: reflect.TypeFor[ResumeUploadSessionRequest](), response: reflect.TypeFor[UploadSessionResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		"signUploadSessionParts": {
			summary: "Create short-lived upload requests for missing parts", tag: "Files", status: http.StatusOK,
			request: reflect.TypeFor[SignUploadPartsRequest](), response: reflect.TypeFor[SignUploadPartsResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"ackUploadSessionParts": {
			summary: "Verify and acknowledge uploaded part ETags", tag: "Files", status: http.StatusOK,
			request: reflect.TypeFor[AckUploadPartsRequest](), response: reflect.TypeFor[UploadSessionResponse](),
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"localUploadSessionPart": {
			summary: "Stream one exact resumable part to Local storage", tag: "Files", status: http.StatusNoContent,
			request: reflect.TypeFor[[]byte](), requestMedia: "application/octet-stream",
			parameters: []*huma.Param{{
				Name: "number", In: "path", Required: true, Description: "One-based multipart part number.",
				Schema: &huma.Schema{Type: "integer", Format: "int32", Minimum: floatPointer(1), Maximum: floatPointer(32)},
			}},
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"completeUploadSession": {
			summary: "Merge, verify, and complete a resumable upload", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[FileResponse](),
			errors:   []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"cancelUploadSession": {
			summary: "Cancel and abort a resumable upload", tag: "Files", status: http.StatusNoContent,
			errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		"localUpload": {
			summary: "Upload content to the development local-storage provider", tag: "Files", status: http.StatusNoContent,
			request: reflect.TypeFor[[]byte](), requestMedia: "application/octet-stream",
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError},
		},
		"localContent": {
			summary: "Read content from the development local-storage provider", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[[]byte](), responseMedia: "*/*",
			errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"confirmUpload": {
			summary: "Verify and confirm an uploaded object", tag: "Files", status: http.StatusOK,
			response: reflect.TypeFor[FileResponse](),
			errors:   []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusInternalServerError},
		},
		"getFileURL": {
			summary: "Create a temporary file access URL", tag: "Files", status: http.StatusOK,
			response:   reflect.TypeFor[SignedRequestResponse](),
			parameters: []*huma.Param{queryEnumParameter("purpose", "Present a verified preview or force a download.", "preview", "download")},
			errors:     []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		},
		"deleteFile": {
			summary: "Schedule file deletion", tag: "Files", status: http.StatusAccepted,
			errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
	}
}

func moduleAPIContract(contract operationContract) module.APIContract {
	parameters := make([]module.ParameterDefinition, 0, len(contract.parameters))
	for _, parameter := range contract.parameters {
		if parameter == nil || parameter.Schema == nil {
			continue
		}
		enum := make([]string, 0, len(parameter.Schema.Enum))
		for _, value := range parameter.Schema.Enum {
			if text, ok := value.(string); ok {
				enum = append(enum, text)
			}
		}
		parameters = append(parameters, module.ParameterDefinition{
			Name:        parameter.Name,
			In:          module.ParameterLocation(parameter.In),
			Description: parameter.Description,
			Required:    parameter.Required,
			Type:        module.ParameterType(parameter.Schema.Type),
			Format:      parameter.Schema.Format,
			Pattern:     parameter.Schema.Pattern,
			Default:     parameter.Schema.Default,
			Enum:        enum,
			Minimum:     parameter.Schema.Minimum,
			Maximum:     parameter.Schema.Maximum,
			MaxLength:   parameter.Schema.MaxLength,
		})
	}
	return module.APIContract{
		Summary:           contract.summary,
		Tag:               contract.tag,
		SuccessStatus:     contract.status,
		RequestDTO:        contract.request,
		RequestMediaType:  contract.requestMedia,
		ResponseDTO:       contract.response,
		ResponseMediaType: contract.responseMedia,
		Parameters:        parameters,
		ErrorStatuses:     contract.errors,
	}
}

func operationContractFromModule(contract module.APIContract) operationContract {
	parameters := make([]*huma.Param, 0, len(contract.Parameters))
	for _, definition := range contract.Parameters {
		enum := make([]any, len(definition.Enum))
		for index, value := range definition.Enum {
			enum[index] = value
		}
		parameters = append(parameters, &huma.Param{
			Name:        definition.Name,
			In:          string(definition.In),
			Description: definition.Description,
			Required:    definition.Required,
			Schema: &huma.Schema{
				Type:      string(definition.Type),
				Format:    definition.Format,
				Pattern:   definition.Pattern,
				Default:   definition.Default,
				Enum:      enum,
				Minimum:   definition.Minimum,
				Maximum:   definition.Maximum,
				MaxLength: definition.MaxLength,
			},
		})
	}
	return operationContract{
		summary:       contract.Summary,
		tag:           contract.Tag,
		status:        contract.SuccessStatus,
		request:       contract.RequestDTO,
		requestMedia:  contract.RequestMediaType,
		response:      contract.ResponseDTO,
		responseMedia: contract.ResponseMediaType,
		parameters:    parameters,
		errors:        contract.ErrorStatuses,
	}
}

func appendUniqueStatuses(existing []int, statuses ...int) []int {
	result := append([]int(nil), existing...)
	for _, status := range statuses {
		found := false
		for _, current := range result {
			if current == status {
				found = true
				break
			}
		}
		if !found {
			result = append(result, status)
		}
	}
	return result
}

func operationResponses(openapi *huma.OpenAPI, contract operationContract) map[string]*huma.Response {
	responses := make(map[string]*huma.Response, len(contract.errors)+2)
	success := &huma.Response{Description: http.StatusText(contract.status)}
	if contract.response != nil {
		mediaType := contract.responseMedia
		if mediaType == "" {
			mediaType = jsonMediaType
		}
		success.Content = map[string]*huma.MediaType{
			mediaType: {Schema: contractSchema(openapi, contract.response, "SuccessResponse")},
		}
	}
	responses[strconv.Itoa(contract.status)] = success
	for _, status := range contract.errors {
		responses[strconv.Itoa(status)] = problemResponse(openapi, status)
	}
	responses["default"] = problemResponse(openapi, 0)
	return responses
}

func problemResponse(openapi *huma.OpenAPI, status int) *huma.Response {
	description := "Problem details response"
	if status != 0 {
		description = http.StatusText(status)
	}
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			problemMediaType: {Schema: contractSchema(openapi, reflect.TypeFor[Problem](), "Problem")},
		},
	}
}

func contractSchema(openapi *huma.OpenAPI, contractType reflect.Type, hint string) *huma.Schema {
	if contractType == reflect.TypeFor[[]byte]() {
		return &huma.Schema{Type: "string", Format: "binary"}
	}
	return openapi.Components.Schemas.Schema(contractType, true, hint)
}

var contractPathParameterPattern = regexp.MustCompile(`\{([^{}]+)\}`)

func pathParameters(path string) []*huma.Param {
	matches := contractPathParameterPattern.FindAllStringSubmatch(path, -1)
	parameters := make([]*huma.Param, 0, len(matches))
	for _, match := range matches {
		name := strings.TrimSuffix(match[1], "+")
		schema := &huma.Schema{Type: "string"}
		if name == "id" {
			schema.Format = "uuid"
		}
		parameters = append(parameters, &huma.Param{
			Name: name, In: "path", Required: true, Schema: schema,
		})
	}
	return parameters
}

func operationParameters(path string, declared []*huma.Param) []*huma.Param {
	declaredPath := make(map[string]*huma.Param)
	other := make([]*huma.Param, 0, len(declared))
	for _, parameter := range declared {
		if parameter.In == "path" {
			declaredPath[parameter.Name] = parameter
			continue
		}
		other = append(other, parameter)
	}

	result := pathParameters(path)
	for index, parameter := range result {
		if replacement, ok := declaredPath[parameter.Name]; ok {
			result[index] = replacement
		}
	}
	return append(result, other...)
}

func pageParameter() *huma.Param {
	minimum := float64(1)
	return &huma.Param{
		Name: "page", In: "query", Description: "One-based page number.",
		Schema: &huma.Schema{Type: "integer", Format: "int32", Default: 1, Minimum: &minimum},
	}
}

func pageSizeParameter() *huma.Param {
	minimum := float64(1)
	maximum := float64(100)
	return &huma.Param{
		Name: "pageSize", In: "query", Description: "Number of records per page.",
		Schema: &huma.Schema{Type: "integer", Format: "int32", Default: 20, Minimum: &minimum, Maximum: &maximum},
	}
}

func queryStringParameter(name, description string) *huma.Param {
	return &huma.Param{
		Name: name, In: "query", Description: description, Schema: &huma.Schema{Type: "string"},
	}
}

func queryEnumParameter(name, description string, values ...string) *huma.Param {
	enum := make([]any, len(values))
	for index, value := range values {
		enum[index] = value
	}
	return &huma.Param{
		Name: name, In: "query", Description: description,
		Schema: &huma.Schema{Type: "string", Enum: enum},
	}
}

func floatPointer(value float64) *float64 { return &value }

func jobStateParameter() *huma.Param {
	parameter := queryEnumParameter(
		"state",
		"Filter by durable job state. Defaults to the dead-letter state.",
		"pending",
		"running",
		"succeeded",
		"failed",
		"dead",
	)
	parameter.Schema.Default = string(jobs.StateDead)
	return parameter
}

func jobTypeParameter() *huma.Param {
	return &huma.Param{
		Name: "type", In: "query", Description: "Filter by exact versioned handler type.",
		Schema: &huma.Schema{
			Type:      "string",
			MaxLength: integerPointer(120),
			Pattern:   `^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`,
		},
	}
}

func idempotencyParameter() *huma.Param {
	return &huma.Param{
		Name: "Idempotency-Key", In: "header",
		Description: "Optional opaque key used to deduplicate a retried write. If the module is disabled, sending this header fails explicitly.",
		Schema:      &huma.Schema{Type: "string", MaxLength: integerPointer(200)},
	}
}

func csrfParameter() *huma.Param {
	return &huma.Param{
		Name:        httpx.CSRFHeaderName,
		In:          "header",
		Description: "Double-submit token obtained from GET /api/v1/auth/csrf.",
		Required:    true,
		Schema: &huma.Schema{
			Type:      "string",
			Pattern:   `^[A-Za-z0-9_-]{43}$`,
			MaxLength: integerPointer(43),
		},
	}
}

func requireCSRFParameter(parameters []*huma.Param) []*huma.Param {
	for _, parameter := range parameters {
		if parameter == nil ||
			parameter.In != "header" ||
			!strings.EqualFold(
				parameter.Name,
				httpx.CSRFHeaderName,
			) {
			continue
		}
		required := csrfParameter()
		*parameter = *required
		return parameters
	}
	return append(parameters, csrfParameter())
}

func operationRequiresCSRF(
	registry *module.Registry,
	definition module.OperationDefinition,
) bool {
	switch definition.Method {
	case module.MethodPost,
		module.MethodPut,
		module.MethodPatch,
		module.MethodDelete:
	default:
		return false
	}
	if definition.Public {
		return true
	}
	scheme, ok := registry.AuthenticationScheme(
		definition.Authentication,
	)
	return ok && scheme.Kind == module.AuthenticationCookie
}

func appendStatus(statuses []int, status int) []int {
	for _, existing := range statuses {
		if existing == status {
			return statuses
		}
	}
	return append(statuses, status)
}

func integerPointer(value int) *int {
	return &value
}

func validateContractCoverage(registry *module.Registry) error {
	for _, operation := range registry.Operations() {
		if _, ok := registry.APIContract(operation.ID); !ok {
			return fmt.Errorf("operation %q has no API contract", operation.ID)
		}
	}
	for _, contract := range registry.APIContracts() {
		id := contract.OperationID
		if _, ok := operationByID(registry, id); !ok {
			return fmt.Errorf("API contract %q has no registered operation", id)
		}
	}
	return nil
}

func operationByID(registry *module.Registry, id string) (module.OperationDefinition, bool) {
	for _, operation := range registry.Operations() {
		if strings.EqualFold(operation.ID, id) {
			return operation, true
		}
	}
	return module.OperationDefinition{}, false
}
