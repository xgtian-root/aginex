package app

import (
	"reflect"
	"testing"

	"github.com/xgtian-root/aginex/backend/framework/module"
)

func TestComposeRegistryDeclaresEveryBuiltInModule(t *testing.T) {
	registry, operations, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	wantModules := []string{
		"access",
		"authentication",
		"files",
		"health",
		"jobs",
		"starter-example",
		"storage-settings",
	}
	if !reflect.DeepEqual(registry.Modules(), wantModules) {
		t.Fatalf("modules = %v, want %v", registry.Modules(), wantModules)
	}
	for _, operationID := range []string{
		"login",
		"logout",
		"revokeAllSessions",
		"listProducts",
		"listFiles",
		"deleteFile",
		"listJobs",
		"retryDeadJob",
		"listStorageProfiles",
	} {
		if _, ok := operations[operationID]; !ok {
			t.Fatalf("operation %q is not registered", operationID)
		}
	}
	if login := operations["login"]; !login.RateLimit.Enabled() ||
		login.RateLimit.Subject != module.RateLimitByIP ||
		login.Idempotency.Enabled() {
		t.Fatalf("login request protection = %#v", login)
	}
	if create := operations["createProduct"]; !create.Idempotency.Enabled() ||
		create.RateLimit.Enabled() {
		t.Fatalf("createProduct request protection = %#v", create)
	}
	if upload := operations["createUploadIntent"]; !upload.Idempotency.Enabled() ||
		!upload.RateLimit.Enabled() ||
		upload.RateLimit.Subject != module.RateLimitByActor {
		t.Fatalf("createUploadIntent request protection = %#v", upload)
	}
	if audit := operations["listAuditLogs"]; audit.Idempotency.Enabled() ||
		!audit.RateLimit.Enabled() ||
		audit.RateLimit.Subject != module.RateLimitByActor {
		t.Fatalf("listAuditLogs request protection = %#v", audit)
	}
	if productList := operations["listProducts"]; productList.Idempotency.Enabled() ||
		productList.RateLimit.Enabled() {
		t.Fatalf("listProducts request protection = %#v", productList)
	}
	revokeAll := operations["revokeAllSessions"]
	if revokeAll.Method != module.MethodDelete ||
		revokeAll.Path != "/api/v1/auth/sessions" ||
		revokeAll.Permission != "sessions:delete" ||
		revokeAll.Policy != policyOwner ||
		revokeAll.Public {
		t.Fatalf("revokeAllSessions contract = %#v", revokeAll)
	}
	resources := registry.Resources()
	if len(resources) != 6 {
		t.Fatalf("resources = %#v, want access, file, and product resource definitions", resources)
	}
	byName := make(map[string]module.ResourceDefinition, len(resources))
	for _, resource := range resources {
		byName[resource.Name] = resource
	}
	file := byName["files"]
	if file.Name != "files" ||
		file.Ownership != module.OwnershipOwner ||
		file.Policy != policyOwner ||
		len(file.Operations) != 13 {
		t.Fatalf("file resource = %#v", file)
	}
	product := byName["products"]
	if product.Name != "products" ||
		product.Ownership != module.OwnershipSystem ||
		product.Policy != policySystem ||
		len(product.Operations) != 5 {
		t.Fatalf("product resource = %#v", product)
	}
	permission := byName["permissions"]
	if permission.Name != "permissions" ||
		permission.Ownership != module.OwnershipSystem ||
		permission.Policy != policySystem ||
		len(permission.Operations) != 1 {
		t.Fatalf("permission resource = %#v", permission)
	}
	role := byName["roles"]
	if role.Name != "roles" ||
		role.Ownership != module.OwnershipSystem ||
		role.Policy != policySystem ||
		len(role.Operations) != 6 {
		t.Fatalf("role resource = %#v", role)
	}
	user := byName["users"]
	if user.Name != "users" ||
		user.Ownership != module.OwnershipSystem ||
		user.Policy != policySystem ||
		len(user.Operations) != 11 {
		t.Fatalf("user resource = %#v", user)
	}
	for _, resource := range []module.ResourceDefinition{
		file,
		permission,
		product,
		role,
		user,
	} {
		for _, operation := range resource.Operations {
			if operation.RequestDTO == resource.Model || operation.ResponseDTO == resource.Model {
				t.Fatalf("%s operation reuses persistence model: %#v", resource.Name, operation)
			}
		}
	}
}

func TestGinPathConvertsContractParameters(t *testing.T) {
	cases := map[string]string{
		"/api/v1/products/{id}":              "/api/v1/products/:id",
		"/api/v1/files/local-upload/{key+}":  "/api/v1/files/local-upload/*key",
		"/api/v1/files/{id}/confirm":         "/api/v1/files/:id/confirm",
		"/api/v1/files/local-content/{key+}": "/api/v1/files/local-content/*key",
		"/api/v1/dashboard/summary":          "/api/v1/dashboard/summary",
	}
	for input, want := range cases {
		if got := ginPath(input); got != want {
			t.Fatalf("ginPath(%q) = %q, want %q", input, got, want)
		}
	}
}
