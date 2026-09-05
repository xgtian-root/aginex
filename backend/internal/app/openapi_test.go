package app

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/framework/module"
)

func TestOperationParametersMergeDeclaredPathSchemaWithoutDuplicates(t *testing.T) {
	declared := &huma.Param{
		Name:        "id",
		In:          "path",
		Description: "Application-specific identifier.",
		Required:    true,
		Schema:      &huma.Schema{Type: "string", Format: "slug"},
	}
	parameters := operationParameters(
		"/api/v1/postmarks/{id}",
		[]*huma.Param{
			declared,
			{
				Name:        "include",
				In:          "query",
				Description: "Related data to include.",
				Schema:      &huma.Schema{Type: "string"},
			},
		},
	)
	if len(parameters) != 2 {
		t.Fatalf("parameters = %#v, want one path and one query parameter", parameters)
	}
	if parameters[0] != declared || parameters[0].Schema.Format != "slug" {
		t.Fatalf("declared path parameter was not preserved: %#v", parameters[0])
	}
}

func TestBuildOpenAPIDoesNotRequireRuntimeDependencies(t *testing.T) {
	document := BuildOpenAPI()
	if document == nil {
		t.Fatal("BuildOpenAPI returned nil")
	}
	for _, path := range []string{
		"/api/v1/health/live",
		"/api/v1/auth/login",
		"/api/v1/products",
		"/api/v1/files",
	} {
		if document.Paths == nil || document.Paths[path] == nil {
			t.Fatalf("OpenAPI path %q is missing", path)
		}
	}
}

func TestOpenAPIContractsCoverRegisteredOperations(t *testing.T) {
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateContractCoverage(registry); err != nil {
		t.Fatal(err)
	}
	document := BuildOpenAPI()
	for _, definition := range registry.Operations() {
		operation := operationAt(document, definition.Method, definition.Path)
		if operation == nil {
			t.Fatalf("%s %s (%s) is missing", definition.Method, definition.Path, definition.ID)
		}
		if definition.ID != "live" && definition.ID != "ready" && operation.OperationID != definition.ID {
			t.Fatalf("%s %s operation ID = %q, want %q", definition.Method, definition.Path, operation.OperationID, definition.ID)
		}
		if definition.Public || definition.ID == "live" || definition.ID == "ready" {
			continue
		}
		if len(operation.Security) != 1 {
			t.Fatalf("%s must declare exactly one session security requirement", definition.ID)
		}
		if _, ok := operation.Security[0][sessionScheme]; !ok {
			t.Fatalf("%s does not declare %s security", definition.ID, sessionScheme)
		}
		if response := operation.Responses["default"]; response == nil ||
			response.Content[problemMediaType] == nil ||
			response.Content[problemMediaType].Schema == nil {
			t.Fatalf("%s has no typed problem response", definition.ID)
		}
	}
}

func TestOpenAPIIncludesTypedBodiesAndDoesNotExposeStorageKeys(t *testing.T) {
	document := BuildOpenAPI()
	login := operationAt(document, module.MethodPost, "/api/v1/auth/login")
	if login == nil || login.RequestBody == nil ||
		login.RequestBody.Content[jsonMediaType] == nil ||
		login.Responses["200"].Content[jsonMediaType] == nil {
		t.Fatal("login must have typed JSON request and response bodies")
	}
	if login.Security == nil || len(login.Security) != 0 {
		t.Fatal("login must explicitly override authentication as a public operation")
	}
	loginSchema := document.Components.Schemas.Map()["LoginRequest"]
	if loginSchema == nil || loginSchema.Properties["password"] == nil {
		t.Fatalf("LoginRequest password schema is missing: %#v", loginSchema)
	}
	loginPassword := loginSchema.Properties["password"]
	if !loginPassword.WriteOnly ||
		loginPassword.MinLength != nil ||
		loginPassword.MaxLength != nil {
		t.Fatalf(
			"LoginRequest password has unsafe metadata or length bounds: %#v",
			loginPassword,
		)
	}

	createProduct := operationAt(document, module.MethodPost, "/api/v1/products")
	if createProduct == nil || createProduct.RequestBody == nil ||
		createProduct.RequestBody.Content[jsonMediaType] == nil ||
		createProduct.Responses["201"].Content[jsonMediaType] == nil {
		t.Fatal("create product must have typed JSON request and response bodies")
	}

	localUpload := operationAt(document, module.MethodPut, "/api/v1/files/local-upload/{key+}")
	if localUpload == nil || localUpload.RequestBody == nil ||
		localUpload.RequestBody.Content["application/octet-stream"] == nil {
		t.Fatal("local upload must document its generic binary body")
	}

	fileSchema := document.Components.Schemas.Map()["FileResponse"]
	if fileSchema == nil {
		t.Fatal("FileResponse schema is missing")
	}
	for _, privateField := range []string{"bucket", "objectKey", "etag"} {
		if _, exposed := fileSchema.Properties[privateField]; exposed {
			t.Fatalf("FileResponse exposes private storage field %q", privateField)
		}
	}
}

func TestOpenAPIDeclaresSessionCookieScheme(t *testing.T) {
	document := BuildOpenAPI()
	scheme := document.Components.SecuritySchemes[sessionScheme]
	if scheme == nil {
		t.Fatal("cookie session security scheme is missing")
	}
	if scheme.Type != "apiKey" ||
		scheme.In != "cookie" ||
		scheme.Name != httpx.SessionCookieName {
		t.Fatalf("unexpected cookie session scheme: %#v", scheme)
	}
}

func TestOpenAPIDeclaresRequiredCSRFForBrowserWritesOnly(
	t *testing.T,
) {
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	document := BuildOpenAPI()
	for _, definition := range registry.Operations() {
		operation := operationAt(
			document,
			definition.Method,
			definition.Path,
		)
		if operation == nil {
			t.Fatalf("operation %q is missing", definition.ID)
		}
		unsafe := definition.Method == module.MethodPost ||
			definition.Method == module.MethodPut ||
			definition.Method == module.MethodPatch ||
			definition.Method == module.MethodDelete
		expected := unsafe && definition.Public
		if !definition.Public && unsafe {
			if definition.Authentication ==
				authenticationCookie {
				expected = true
			} else {
				scheme, ok := registry.AuthenticationScheme(
					definition.Authentication,
				)
				expected = ok &&
					scheme.Kind == module.AuthenticationCookie
			}
		}
		parameter := headerParameter(
			operation,
			httpx.CSRFHeaderName,
		)
		if (parameter != nil) != expected {
			t.Fatalf(
				"operation %q CSRF declaration = %t, want %t",
				definition.ID,
				parameter != nil,
				expected,
			)
		}
		if parameter != nil &&
			(!parameter.Required ||
				parameter.Schema == nil ||
				parameter.Schema.Pattern == "") {
			t.Fatalf(
				"operation %q has a weak CSRF contract: %#v",
				definition.ID,
				parameter,
			)
		}
		if expected && operation.Responses["403"] == nil {
			t.Fatalf(
				"operation %q is missing its CSRF 403 response",
				definition.ID,
			)
		}
	}

	bearerDocument := BuildOpenAPIWithModules(
		bearerExtensionModule{},
	)
	bearerWrite := operationAt(
		bearerDocument,
		module.MethodPost,
		"/api/v1/test-bearer",
	)
	if bearerWrite == nil {
		t.Fatal("bearer write operation is missing")
	}
	if parameter := headerParameter(
		bearerWrite,
		httpx.CSRFHeaderName,
	); parameter != nil {
		t.Fatalf(
			"Bearer write unexpectedly requires CSRF: %#v",
			parameter,
		)
	}

	login := operationAt(
		document,
		module.MethodPost,
		"/api/v1/auth/login",
	)
	if login == nil ||
		headerParameter(login, httpx.CSRFHeaderName) == nil {
		t.Fatal("cookie-issuing login does not document CSRF")
	}
}

func TestOpenAPIDocumentsRateLimitAndFileVerificationFailures(t *testing.T) {
	document := BuildOpenAPI()
	for _, target := range []struct {
		method module.HTTPMethod
		path   string
	}{
		{module.MethodPost, "/api/v1/auth/login"},
		{module.MethodPut, "/api/v1/files/local-upload/{key+}"},
		{module.MethodGet, "/api/v1/audit-logs"},
	} {
		operation := operationAt(document, target.method, target.path)
		if operation == nil || operation.Responses["429"] == nil || operation.Responses["503"] == nil {
			t.Fatalf("%s %s is missing explicit rate-limit responses", target.method, target.path)
		}
	}
	confirm := operationAt(document, module.MethodPost, "/api/v1/files/{id}/confirm")
	if confirm == nil || confirm.Responses["422"] == nil || confirm.Responses["413"] == nil {
		t.Fatal("upload confirmation is missing explicit content-verification responses")
	}
	intent := operationAt(document, module.MethodPost, "/api/v1/files/upload-intents")
	if intent == nil || intent.Responses["422"] == nil || intent.Responses["413"] == nil {
		t.Fatal("upload intent is missing explicit policy and resumable-strategy responses")
	}
}

func TestOpenAPIIdempotencyHeadersMatchEnforcedOperations(t *testing.T) {
	document := BuildOpenAPI()
	targets := []struct {
		method module.HTTPMethod
		path   string
	}{
		{module.MethodPost, "/api/v1/products"},
		{module.MethodPut, "/api/v1/products/{id}"},
		{module.MethodDelete, "/api/v1/products/{id}"},
		{module.MethodPost, "/api/v1/files/upload-intents"},
		{module.MethodPost, "/api/v1/files/{id}/confirm"},
		{module.MethodDelete, "/api/v1/files/{id}"},
	}
	for _, target := range targets {
		operation := operationAt(document, target.method, target.path)
		if operation == nil {
			t.Fatalf("%s %s is missing", target.method, target.path)
		}
		var parameter *huma.Param
		for _, candidate := range operation.Parameters {
			if candidate.Name == idempotencyHeader && candidate.In == "header" {
				parameter = candidate
				break
			}
		}
		if parameter == nil || parameter.Schema == nil ||
			parameter.Schema.MaxLength == nil ||
			*parameter.Schema.MaxLength != 200 {
			t.Fatalf(
				"%s %s has invalid Idempotency-Key contract: %#v",
				target.method,
				target.path,
				parameter,
			)
		}
		for _, status := range []string{"400", "409", "503"} {
			if operation.Responses[status] == nil {
				t.Fatalf(
					"%s %s is missing idempotency response %s",
					target.method,
					target.path,
					status,
				)
			}
		}
	}
}

func TestOpenAPIRequestProtectionFollowsOperationDeclarations(t *testing.T) {
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	document := BuildOpenAPI()
	for _, definition := range registry.Operations() {
		operation := operationAt(
			document,
			definition.Method,
			definition.Path,
		)
		if operation == nil {
			t.Fatalf("operation %q is missing from OpenAPI", definition.ID)
		}
		var hasIdempotencyParameter bool
		for _, parameter := range operation.Parameters {
			if parameter.Name == idempotencyHeader &&
				parameter.In == "header" {
				hasIdempotencyParameter = true
				break
			}
		}
		if hasIdempotencyParameter != definition.Idempotency.Enabled() {
			t.Fatalf(
				"operation %q idempotency declaration/OpenAPI mismatch: %t/%t",
				definition.ID,
				definition.Idempotency.Enabled(),
				hasIdempotencyParameter,
			)
		}
		if definition.RateLimit.Enabled() &&
			operation.Responses["429"] == nil {
			t.Fatalf(
				"rate-limited operation %q is missing its 429 response",
				definition.ID,
			)
		}
	}
}

func operationAt(
	document *huma.OpenAPI,
	method module.HTTPMethod,
	path string,
) *huma.Operation {
	item := document.Paths[path]
	if item == nil {
		return nil
	}
	switch method {
	case module.MethodGet:
		return item.Get
	case module.MethodPost:
		return item.Post
	case module.MethodPut:
		return item.Put
	case module.MethodPatch:
		return item.Patch
	case module.MethodDelete:
		return item.Delete
	case module.MethodHead:
		return item.Head
	case module.MethodOptions:
		return item.Options
	default:
		return nil
	}
}

func headerParameter(
	operation *huma.Operation,
	name string,
) *huma.Param {
	if operation == nil {
		return nil
	}
	for _, parameter := range operation.Parameters {
		if parameter.In == "header" &&
			http.CanonicalHeaderKey(parameter.Name) ==
				http.CanonicalHeaderKey(name) {
			return parameter
		}
	}
	return nil
}
