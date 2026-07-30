package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

type documentedOperation struct {
	method  string
	path    string
	id      string
	summary string
	tag     string
	status  int
}

func documentGinOperations(openapi *huma.OpenAPI) {
	operations := []documentedOperation{
		{http.MethodPost, "/api/v1/auth/login", "login", "Sign in with a local account", "Authentication", http.StatusOK},
		{http.MethodPost, "/api/v1/auth/logout", "logout", "Revoke the current session", "Authentication", http.StatusNoContent},
		{http.MethodGet, "/api/v1/auth/me", "getCurrentUser", "Get the current principal and permissions", "Authentication", http.StatusOK},
		{http.MethodGet, "/api/v1/dashboard/summary", "getDashboardSummary", "Get workspace summary metrics", "Dashboard", http.StatusOK},
		{http.MethodGet, "/api/v1/products", "listProducts", "List and search products", "Products", http.StatusOK},
		{http.MethodPost, "/api/v1/products", "createProduct", "Create a product", "Products", http.StatusCreated},
		{http.MethodGet, "/api/v1/products/{id}", "getProduct", "Get a product", "Products", http.StatusOK},
		{http.MethodPut, "/api/v1/products/{id}", "updateProduct", "Update a product", "Products", http.StatusOK},
		{http.MethodDelete, "/api/v1/products/{id}", "deleteProduct", "Delete a product", "Products", http.StatusNoContent},
		{http.MethodGet, "/api/v1/users", "listUsers", "List users", "Access", http.StatusOK},
		{http.MethodGet, "/api/v1/roles", "listRoles", "List roles and grants", "Access", http.StatusOK},
		{http.MethodGet, "/api/v1/permissions", "listPermissions", "List permission definitions", "Access", http.StatusOK},
		{http.MethodGet, "/api/v1/audit-logs", "listAuditLogs", "List audit events", "Audit", http.StatusOK},
		{http.MethodGet, "/api/v1/files", "listFiles", "List file metadata", "Files", http.StatusOK},
		{http.MethodPost, "/api/v1/files/upload-intents", "createUploadIntent", "Create a direct upload intent", "Files", http.StatusCreated},
		{http.MethodPost, "/api/v1/files/{id}/confirm", "confirmUpload", "Verify and confirm an uploaded object", "Files", http.StatusOK},
		{http.MethodGet, "/api/v1/files/{id}/url", "getFileURL", "Create a temporary file access URL", "Files", http.StatusOK},
		{http.MethodDelete, "/api/v1/files/{id}", "deleteFile", "Delete a file and its metadata", "Files", http.StatusNoContent},
	}
	for _, item := range operations {
		responseDescription := http.StatusText(item.status)
		operation := &huma.Operation{
			Method: item.method, Path: item.path, OperationID: item.id, Summary: item.summary,
			Tags: []string{item.tag},
			Responses: map[string]*huma.Response{
				strconv.Itoa(item.status): {Description: responseDescription},
				"default":                 {Description: "Problem details response"},
			},
		}
		if strings.Contains(item.path, "{id}") {
			operation.Parameters = []*huma.Param{{
				Name: "id", In: "path", Required: true,
				Schema: &huma.Schema{Type: "string", Format: "uuid"},
			}}
		}
		openapi.AddOperation(operation)
	}
}
