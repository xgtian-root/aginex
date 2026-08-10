package setup

import (
	"net/http"
	"reflect"
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
	database := schemas["DatabaseInput"]
	postgres := schemas["PostgresDatabaseInput"]
	mysql := schemas["MySQLDatabaseInput"]
	administrator := schemas["AdministratorConfig"]
	assertDatabaseInputUnion(t, schemas, database)
	if postgres == nil || postgres.Properties["password"] == nil ||
		!postgres.Properties["password"].WriteOnly ||
		postgres.Properties["password"].MinLength == nil ||
		*postgres.Properties["password"].MinLength != 1 {
		t.Fatalf("PostgreSQL password schema is not protected: %#v", postgres)
	}
	if mysql == nil || mysql.Properties["password"] == nil ||
		!mysql.Properties["password"].WriteOnly ||
		mysql.Properties["password"].MinLength == nil ||
		*mysql.Properties["password"].MinLength != 1 {
		t.Fatalf("MySQL password schema is not protected: %#v", mysql)
	}
	if administrator == nil || administrator.Properties["password"] == nil {
		t.Fatalf("administrator password schema is missing: %#v", administrator)
	}
	administratorPassword := administrator.Properties["password"]
	if !administratorPassword.WriteOnly ||
		administratorPassword.MinLength != nil ||
		administratorPassword.MaxLength != nil {
		t.Fatalf(
			"administrator password schema has unsafe metadata or length bounds: %#v",
			administratorPassword,
		)
	}
}

func TestDatabaseInputOpenAPISchemaRejectsMismatchedVariants(t *testing.T) {
	config := huma.DefaultConfig("Aginex API", "1.0.0")
	document := config.OpenAPI
	DocumentOpenAPI(document)
	database := document.Components.Schemas.Map()["DatabaseInput"]
	if database == nil {
		t.Fatal("DatabaseInput schema is missing")
	}

	sqlite := map[string]any{
		"directory": "/var/lib/aginex",
		"filename":  "aginex.db",
	}
	postgres := map[string]any{
		"host":     "db.example.test",
		"port":     float64(5432),
		"database": "aginex",
		"username": "aginex",
		"password": "secret",
		"sslMode":  "require",
	}
	mysql := map[string]any{
		"host":     "db.example.test",
		"port":     float64(3306),
		"database": "aginex",
		"username": "aginex",
		"password": "secret",
		"tlsMode":  "required",
	}
	tests := []struct {
		name  string
		value map[string]any
		valid bool
	}{
		{
			name:  "sqlite",
			value: map[string]any{"driver": "sqlite", "sqlite": sqlite},
			valid: true,
		},
		{
			name:  "postgres",
			value: map[string]any{"driver": "postgres", "postgres": postgres},
			valid: true,
		},
		{
			name:  "mysql",
			value: map[string]any{"driver": "mysql", "mysql": mysql},
			valid: true,
		},
		{
			name:  "matching object missing",
			value: map[string]any{"driver": "sqlite"},
		},
		{
			name:  "driver and object mismatch",
			value: map[string]any{"driver": "sqlite", "postgres": postgres},
		},
		{
			name: "multiple driver objects",
			value: map[string]any{
				"driver": "sqlite", "sqlite": sqlite, "mysql": mysql,
			},
		},
		{
			name:  "unknown driver",
			value: map[string]any{"driver": "cockroach", "postgres": postgres},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := &huma.ValidateResult{}
			huma.Validate(
				document.Components.Schemas,
				database,
				huma.NewPathBuffer(nil, 0),
				huma.ModeWriteToServer,
				test.value,
				result,
			)
			if test.valid && len(result.Errors) != 0 {
				t.Fatalf("valid input errors = %v", result.Errors)
			}
			if !test.valid && len(result.Errors) == 0 {
				t.Fatal("invalid input unexpectedly matched DatabaseInput schema")
			}
		})
	}
}

func assertDatabaseInputUnion(
	t *testing.T,
	schemas map[string]*huma.Schema,
	database *huma.Schema,
) {
	t.Helper()
	if database == nil {
		t.Fatal("DatabaseInput schema is missing")
	}
	if database.Type != "" || len(database.Properties) != 0 ||
		database.AdditionalProperties != nil {
		t.Fatalf("DatabaseInput retained its permissive base object: %#v", database)
	}
	if database.Discriminator == nil ||
		database.Discriminator.PropertyName != "driver" {
		t.Fatalf("DatabaseInput discriminator = %#v", database.Discriminator)
	}

	variants := []struct {
		driver    string
		component string
		nested    string
		nestedRef string
	}{
		{"sqlite", "DatabaseInputSQLite", "sqlite", "SQLiteDatabaseInput"},
		{"postgres", "DatabaseInputPostgres", "postgres", "PostgresDatabaseInput"},
		{"mysql", "DatabaseInputMySQL", "mysql", "MySQLDatabaseInput"},
	}
	if len(database.OneOf) != len(variants) {
		t.Fatalf("DatabaseInput oneOf = %#v", database.OneOf)
	}
	for index, expected := range variants {
		ref := "#/components/schemas/" + expected.component
		if database.OneOf[index].Ref != ref {
			t.Fatalf("DatabaseInput oneOf[%d] ref = %q", index, database.OneOf[index].Ref)
		}
		if database.Discriminator.Mapping[expected.driver] != ref {
			t.Fatalf(
				"DatabaseInput discriminator mapping[%q] = %q",
				expected.driver,
				database.Discriminator.Mapping[expected.driver],
			)
		}

		variant := schemas[expected.component]
		if variant == nil || variant.Type != huma.TypeObject ||
			variant.AdditionalProperties != false ||
			len(variant.Properties) != 2 ||
			variant.Properties[expected.nested] == nil ||
			variant.Properties["driver"] == nil {
			t.Fatalf("%s schema is not closed: %#v", expected.component, variant)
		}
		if !reflect.DeepEqual(variant.Required, []string{"driver", expected.nested}) {
			t.Fatalf("%s required = %#v", expected.component, variant.Required)
		}
		if !reflect.DeepEqual(
			variant.Properties["driver"].Enum,
			[]any{expected.driver},
		) {
			t.Fatalf(
				"%s driver enum = %#v",
				expected.component,
				variant.Properties["driver"].Enum,
			)
		}
		if variant.Properties[expected.nested].Ref !=
			"#/components/schemas/"+expected.nestedRef {
			t.Fatalf(
				"%s nested ref = %q",
				expected.component,
				variant.Properties[expected.nested].Ref,
			)
		}
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
