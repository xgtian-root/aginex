package setup

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/xgtian-root/aginex/framework/httpx"
)

func TestDocumentOpenAPIAddsSetupContractWithoutRuntimeRegistration(t *testing.T) {
	config := huma.DefaultConfig("Aginex API", "1.0.0")
	document := config.OpenAPI
	DocumentOpenAPI(document)

	tests := []struct {
		path        string
		method      string
		operationID string
		status      string
	}{
		{SystemModePath, http.MethodGet, GetSystemModeOperationID, "200"},
		{SetupStatusPath, http.MethodGet, GetSetupStatusOperationID, "200"},
		{SetupDatabaseTestPath, http.MethodPost, TestSetupDatabaseOperationID, "200"},
		{SetupCompletePath, http.MethodPost, CompleteSetupOperationID, "202"},
	}
	for _, test := range tests {
		operation := documentedPathOperation(t, document, test.path, test.method)
		if operation.OperationID != test.operationID {
			t.Fatalf("%s operation ID = %q", test.path, operation.OperationID)
		}
		if operation.Responses[test.status] == nil ||
			operation.Responses["default"] == nil {
			t.Fatalf("%s responses = %#v", test.path, operation.Responses)
		}
		problem := operation.Responses["default"].Content[problemMediaType]
		if problem == nil || problem.Schema == nil {
			t.Fatalf("%s default Problem response missing", test.path)
		}
	}

	for _, path := range []string{SetupDatabaseTestPath, SetupCompletePath} {
		operation := documentedPathOperation(t, document, path, http.MethodPost)
		if operation.RequestBody == nil || !operation.RequestBody.Required {
			t.Fatalf("%s request body missing", path)
		}
		if operation.RequestBody.Content[jsonMediaType] == nil {
			t.Fatalf("%s JSON request body missing", path)
		}
		if len(operation.Parameters) != 1 ||
			operation.Parameters[0].Name != httpx.CSRFHeaderName ||
			!operation.Parameters[0].Required {
			t.Fatalf("%s CSRF parameter = %#v", path, operation.Parameters)
		}
	}

	schemas := document.Components.Schemas.Map()
	database := schemas["DatabaseConfig"]
	administrator := schemas["AdministratorConfig"]
	if database == nil || database.Properties["dsn"] == nil ||
		!database.Properties["dsn"].WriteOnly {
		t.Fatalf("database DSN schema is not write-only: %#v", database)
	}
	if administrator == nil || administrator.Properties["password"] == nil ||
		!administrator.Properties["password"].WriteOnly {
		t.Fatalf("administrator password schema is not write-only: %#v", administrator)
	}
}

func documentedPathOperation(
	t *testing.T,
	document *huma.OpenAPI,
	path string,
	method string,
) *huma.Operation {
	t.Helper()
	item := document.Paths[path]
	if item == nil {
		t.Fatalf("path %s missing", path)
	}
	switch method {
	case http.MethodGet:
		if item.Get == nil {
			t.Fatalf("GET %s missing", path)
		}
		return item.Get
	case http.MethodPost:
		if item.Post == nil {
			t.Fatalf("POST %s missing", path)
		}
		return item.Post
	default:
		t.Fatalf("unsupported test method %s", method)
		return nil
	}
}
