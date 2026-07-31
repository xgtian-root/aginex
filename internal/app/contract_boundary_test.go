package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
)

type responseContractBoundaryModule struct{}

func (responseContractBoundaryModule) Name() string {
	return "response-contract-boundary"
}

func (responseContractBoundaryModule) Register(
	registry *module.Registry,
) error {
	const (
		authentication = module.AuthenticationRef("invalidProblemAuth")
		permission     = "contract-boundary:read"
		policyRef      = module.PolicyRef("contract-boundary.all")
	)
	policy, err := frameworkauthz.NewAllScopePolicy()
	if err != nil {
		return err
	}
	if err := registry.RegisterPermission(module.PermissionDefinition{
		Code:        permission,
		Description: "Exercise the response contract boundary",
	}); err != nil {
		return err
	}
	if err := registry.RegisterAuthorizationPolicy(
		module.AuthorizationPolicy{Ref: policyRef, Policy: policy},
	); err != nil {
		return err
	}
	if err := registry.RegisterAuthenticationScheme(
		module.AuthenticationScheme{
			Ref:         authentication,
			Kind:        module.AuthenticationBearer,
			Description: "Returns an intentionally invalid test error.",
			Middleware: func(c *gin.Context) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"secret": "authentication-error-must-not-leak",
				})
				c.Abort()
			},
		},
	); err != nil {
		return err
	}

	type operationFixture struct {
		id       string
		path     string
		errors   []int
		public   bool
		handler  gin.HandlerFunc
		authMode module.AuthorizationMode
	}
	fixtures := []operationFixture{
		{
			id:     "declaredContractProblem",
			path:   "/api/v1/test-contract-errors/declared",
			errors: []int{http.StatusConflict, http.StatusInternalServerError},
			public: true,
			handler: func(c *gin.Context) {
				httpx.WriteProblem(
					c,
					http.StatusConflict,
					"DECLARED_TEST_ERROR",
					"Declared test error",
					"The declared error is valid.",
				)
			},
		},
		{
			id:     "undeclaredContractProblem",
			path:   "/api/v1/test-contract-errors/undeclared",
			errors: []int{http.StatusInternalServerError},
			public: true,
			handler: func(c *gin.Context) {
				httpx.WriteProblem(
					c,
					http.StatusConflict,
					"UNDECLARED_TEST_ERROR",
					"Undeclared test error",
					"undeclared-error-must-not-leak",
				)
			},
		},
		{
			id:     "wrongMediaContractProblem",
			path:   "/api/v1/test-contract-errors/media",
			errors: []int{http.StatusConflict, http.StatusInternalServerError},
			public: true,
			handler: func(c *gin.Context) {
				c.JSON(http.StatusConflict, gin.H{
					"secret": "wrong-media-error-must-not-leak",
				})
			},
		},
		{
			id:     "invalidBodyContractProblem",
			path:   "/api/v1/test-contract-errors/body",
			errors: []int{http.StatusConflict, http.StatusInternalServerError},
			public: true,
			handler: func(c *gin.Context) {
				c.Header("Content-Type", httpx.ProblemMediaType)
				c.JSON(http.StatusConflict, Problem{
					Type:      "about:blank",
					Title:     "Invalid test problem",
					Status:    http.StatusBadRequest,
					Detail:    "invalid-problem-must-not-leak",
					Instance:  c.Request.URL.Path,
					Code:      "INVALID_TEST_PROBLEM",
					RequestID: c.Writer.Header().Get("X-Request-ID"),
				})
			},
		},
		{
			id:       "invalidAuthenticationProblem",
			path:     "/api/v1/test-contract-errors/authentication",
			errors:   []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
			authMode: module.AuthorizationAll,
			handler: func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			},
		},
	}
	for _, fixture := range fixtures {
		operation := module.OperationDefinition{
			ID:     fixture.id,
			Method: module.MethodGet,
			Path:   fixture.path,
			Public: fixture.public,
		}
		if !fixture.public {
			operation.Authentication = authentication
			operation.Permission = permission
			operation.Policy = policyRef
		}
		if err := registry.RegisterOperation(operation); err != nil {
			return err
		}
		if err := registry.RegisterAPIContract(module.APIContract{
			OperationID:   fixture.id,
			Summary:       "Exercise response contract enforcement",
			Tag:           "Test extension",
			SuccessStatus: http.StatusNoContent,
			ErrorStatuses: fixture.errors,
		}); err != nil {
			return err
		}
		route := module.HTTPRoute{OperationID: fixture.id}
		if fixture.public {
			route.Handler = fixture.handler
		} else {
			handler := fixture.handler
			route.Authorization = fixture.authMode
			route.AuthorizedHandler = func(
				c *gin.Context,
				_ module.RequestAuthorization,
			) {
				handler(c)
			}
		}
		if err := registry.RegisterHTTPRoute(route); err != nil {
			return err
		}
	}
	const binaryOperationID = "streamContractBinary"
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:     binaryOperationID,
		Method: module.MethodGet,
		Path:   "/api/v1/test-contract-errors/binary",
		Public: true,
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:       binaryOperationID,
		Summary:           "Stream a contract-validated binary response",
		Tag:               "Test extension",
		SuccessStatus:     http.StatusOK,
		ResponseDTO:       reflect.TypeFor[[]byte](),
		ResponseMediaType: "application/octet-stream",
		ErrorStatuses:     []int{http.StatusInternalServerError},
	}); err != nil {
		return err
	}
	return registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID: binaryOperationID,
		Handler: func(c *gin.Context) {
			c.Data(
				http.StatusOK,
				"application/octet-stream",
				[]byte(strings.Repeat("x", (1<<20)+1)),
			)
		},
	})
}

func TestOperationResponseContractAcceptsOnlyDeclaredValidProblems(
	t *testing.T,
) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	server, err := NewWithModules(
		cfg,
		db,
		responseContractBoundaryModule{},
	)
	if err != nil {
		t.Fatal(err)
	}

	declared := serveContractBoundaryRequest(
		t,
		server,
		"/api/v1/test-contract-errors/declared",
	)
	assertProblemCode(
		t,
		declared,
		http.StatusConflict,
		"DECLARED_TEST_ERROR",
	)
	binary := serveContractBoundaryRequest(
		t,
		server,
		"/api/v1/test-contract-errors/binary",
	)
	if binary.Code != http.StatusOK || binary.Body.Len() != (1<<20)+1 {
		t.Fatalf(
			"binary status/size = %d/%d; body = %s",
			binary.Code,
			binary.Body.Len(),
			binary.Body.String(),
		)
	}

	for _, test := range []struct {
		name     string
		path     string
		sentinel string
	}{
		{
			name:     "undeclared status",
			path:     "/api/v1/test-contract-errors/undeclared",
			sentinel: "undeclared-error-must-not-leak",
		},
		{
			name:     "wrong media type",
			path:     "/api/v1/test-contract-errors/media",
			sentinel: "wrong-media-error-must-not-leak",
		},
		{
			name:     "invalid problem body",
			path:     "/api/v1/test-contract-errors/body",
			sentinel: "invalid-problem-must-not-leak",
		},
		{
			name:     "authentication middleware before request validation",
			path:     "/api/v1/test-contract-errors/authentication",
			sentinel: "authentication-error-must-not-leak",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveContractBoundaryRequest(t, server, test.path)
			assertProblemCode(
				t,
				response,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
			)
			if strings.Contains(response.Body.String(), test.sentinel) {
				t.Fatalf(
					"invalid response escaped the contract boundary: %s",
					response.Body.String(),
				)
			}
		})
	}
}

type singleSourceRequest struct {
	Name string `json:"name" binding:"required,min=3"`
}

type requestContractBoundaryModule struct {
	bodyWasConsumed *atomic.Bool
}

func (requestContractBoundaryModule) Name() string {
	return "request-contract-boundary"
}

func (item requestContractBoundaryModule) Register(
	registry *module.Registry,
) error {
	const operationID = "consumeValidatedRequestOnce"
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:     operationID,
		Method: module.MethodPost,
		Path:   "/api/v1/test-contract-request-source",
		Public: true,
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   operationID,
		Summary:       "Consume one validated request DTO",
		Tag:           "Test extension",
		SuccessStatus: http.StatusNoContent,
		RequestDTO:    reflect.TypeFor[singleSourceRequest](),
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
			input, ok := module.ValidatedRequestDTOFromContext[singleSourceRequest](c.Request.Context())
			if !ok || input.Name != "valid" {
				c.Status(http.StatusInternalServerError)
				return
			}
			remaining, err := io.ReadAll(c.Request.Body)
			if err != nil || len(remaining) != 0 {
				c.Status(http.StatusInternalServerError)
				return
			}
			item.bodyWasConsumed.Store(true)
			c.Status(http.StatusNoContent)
		},
	})
}

func TestStructuredRequestBodyHasOneValidatedSource(t *testing.T) {
	cfg := moduleTestConfig(t, config.Bootstrap{})
	db := openMigratedDatabase(t, cfg.Database)
	var bodyWasConsumed atomic.Bool
	server, err := NewWithModules(
		cfg,
		db,
		requestContractBoundaryModule{
			bodyWasConsumed: &bodyWasConsumed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test-contract-request-source",
		strings.NewReader(`{"name":"valid"}`),
	)
	request.Header.Set("Content-Type", jsonMediaType)
	addTestCSRF(request)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if !bodyWasConsumed.Load() {
		t.Fatal("handler did not consume the contract-validated DTO")
	}
}

func TestUploadIntentRuntimeValidationMatchesDeclaredDTOConstraints(
	t *testing.T,
) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	tests := []map[string]any{
		{
			"filename":    strings.Repeat("x", 501),
			"contentType": "image/png",
			"size":        1,
			"visibility":  "private",
		},
		{
			"filename":    "invalid.svg",
			"contentType": "image/svg+xml",
			"size":        1,
			"visibility":  "private",
		},
		{
			"filename":    "too-large.png",
			"contentType": "image/png",
			"size":        int64(10485761),
			"visibility":  "private",
		},
	}
	for index, payload := range tests {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		response := serveRequest(
			server,
			cookie,
			http.MethodPost,
			"/api/v1/files/upload-intents",
			body,
			jsonMediaType,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"case %d status = %d, body = %s",
				index,
				response.Code,
				response.Body.String(),
			)
		}
	}
	var count int64
	if err := db.Model(&domain.FileObject{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("file rows = %d, want 0 after DTO validation failures", count)
	}
}

func serveContractBoundaryRequest(
	t *testing.T,
	server *App,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
