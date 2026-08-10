package setup

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/xgtian-root/aginex/framework/httpx"
)

const (
	jsonMediaType    = "application/json"
	problemMediaType = "application/problem+json"
)

type documentedOperation struct {
	method      string
	path        string
	operationID string
	summary     string
	status      int
	request     reflect.Type
	response    reflect.Type
	errors      []int
	csrf        bool
}

// These named documentation-only variants make DatabaseInput a closed,
// discriminated union without changing the runtime JSON decoding shape.
type databaseInputSQLite struct {
	Driver string              `json:"driver" enum:"sqlite"`
	SQLite SQLiteDatabaseInput `json:"sqlite"`
}

type databaseInputPostgres struct {
	Driver   string                `json:"driver" enum:"postgres"`
	Postgres PostgresDatabaseInput `json:"postgres"`
}

type databaseInputMySQL struct {
	Driver string             `json:"driver" enum:"mysql"`
	MySQL  MySQLDatabaseInput `json:"mysql"`
}

var _ huma.SchemaTransformer = (*DatabaseInput)(nil)

// TransformSchema replaces DatabaseInput's permissive pointer-based object
// schema with three closed variants. DatabaseInput remains the registered
// component type, so runtime decoding and existing request contracts are
// unchanged while generated clients receive a discriminated union.
func (*DatabaseInput) TransformSchema(
	registry huma.Registry,
	_ *huma.Schema,
) *huma.Schema {
	sqlite := registry.Schema(
		reflect.TypeFor[databaseInputSQLite](),
		true,
		"DatabaseInputSQLite",
	)
	postgres := registry.Schema(
		reflect.TypeFor[databaseInputPostgres](),
		true,
		"DatabaseInputPostgres",
	)
	mysql := registry.Schema(
		reflect.TypeFor[databaseInputMySQL](),
		true,
		"DatabaseInputMySQL",
	)

	return &huma.Schema{
		OneOf: []*huma.Schema{sqlite, postgres, mysql},
		Discriminator: &huma.Discriminator{
			PropertyName: "driver",
			Mapping: map[string]string{
				"sqlite":   sqlite.Ref,
				"postgres": postgres.Ref,
				"mysql":    mysql.Ref,
			},
		},
	}
}

// DocumentOpenAPI adds the mode probe and setup-only wire contract to a
// release OpenAPI document. It only mutates the document: runtime application
// routers must not register setup handlers from this function.
func DocumentOpenAPI(openapi *huma.OpenAPI) {
	if openapi == nil {
		panic("setup OpenAPI document is required")
	}
	if openapi.Components.Schemas == nil {
		openapi.Components.Schemas = huma.NewMapRegistry(
			"#/components/schemas/",
			huma.DefaultSchemaNamer,
		)
	}
	operations := []documentedOperation{
		{
			method: http.MethodGet, path: SystemModePath,
			operationID: GetSystemModeOperationID,
			summary:     "Get the active server mode",
			status:      http.StatusOK,
			response:    reflect.TypeFor[SystemModeResponse](),
			errors:      []int{http.StatusInternalServerError},
		},
		{
			method: http.MethodGet, path: SetupStatusPath,
			operationID: GetSetupStatusOperationID,
			summary:     "Get one-time setup progress",
			status:      http.StatusOK,
			response:    reflect.TypeFor[SetupStatusResponse](),
			errors: []int{
				http.StatusNotFound,
				http.StatusInternalServerError,
			},
		},
		{
			method: http.MethodPost, path: SetupDatabaseTestPath,
			operationID: TestSetupDatabaseOperationID,
			summary:     "Test setup database connectivity",
			status:      http.StatusOK,
			request:     reflect.TypeFor[SetupDatabaseTestRequest](),
			response:    reflect.TypeFor[SetupDatabaseTestResponse](),
			csrf:        true,
			errors: []int{
				http.StatusBadRequest,
				http.StatusForbidden,
				http.StatusNotFound,
				http.StatusRequestEntityTooLarge,
				http.StatusUnsupportedMediaType,
				http.StatusUnprocessableEntity,
				http.StatusTooManyRequests,
				http.StatusInternalServerError,
			},
		},
		{
			method: http.MethodPost, path: SetupCompletePath,
			operationID: CompleteSetupOperationID,
			summary:     "Initialize and activate the application",
			status:      http.StatusAccepted,
			request:     reflect.TypeFor[SetupCompleteInput](),
			response:    reflect.TypeFor[SetupAcceptedResponse](),
			csrf:        true,
			errors: []int{
				http.StatusBadRequest,
				http.StatusForbidden,
				http.StatusNotFound,
				http.StatusConflict,
				http.StatusRequestEntityTooLarge,
				http.StatusUnsupportedMediaType,
				http.StatusTooManyRequests,
				http.StatusInternalServerError,
			},
		},
	}
	for _, definition := range operations {
		operation := &huma.Operation{
			Method:      definition.method,
			Path:        definition.path,
			OperationID: definition.operationID,
			Summary:     definition.summary,
			Tags:        []string{"Setup"},
			Security:    []map[string][]string{},
			Responses: setupOperationResponses(
				openapi,
				definition,
			),
		}
		if definition.csrf {
			operation.Parameters = []*huma.Param{{
				Name:        httpx.CSRFHeaderName,
				In:          "header",
				Description: "Double-submit CSRF token",
				Required:    true,
				Schema: &huma.Schema{
					Type:      "string",
					MinLength: intPointer(43),
					MaxLength: intPointer(43),
				},
			}}
		}
		if definition.request != nil {
			operation.RequestBody = &huma.RequestBody{
				Required: true,
				Content: map[string]*huma.MediaType{
					jsonMediaType: {
						Schema: openapi.Components.Schemas.Schema(
							definition.request,
							true,
							definition.operationID+"Request",
						),
					},
				},
			}
		}
		openapi.AddOperation(operation)
	}
}

func setupOperationResponses(
	openapi *huma.OpenAPI,
	definition documentedOperation,
) map[string]*huma.Response {
	responses := make(map[string]*huma.Response, len(definition.errors)+2)
	responses[strconv.Itoa(definition.status)] = &huma.Response{
		Description: http.StatusText(definition.status),
		Content: map[string]*huma.MediaType{
			jsonMediaType: {
				Schema: openapi.Components.Schemas.Schema(
					definition.response,
					true,
					definition.operationID+"Response",
				),
			},
		},
	}
	for _, status := range definition.errors {
		responses[strconv.Itoa(status)] = setupProblemResponse(openapi, status)
	}
	responses["default"] = setupProblemResponse(openapi, 0)
	return responses
}

func setupProblemResponse(openapi *huma.OpenAPI, status int) *huma.Response {
	description := "Problem details response"
	if status != 0 {
		description = http.StatusText(status)
	}
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			problemMediaType: {
				Schema: openapi.Components.Schemas.Schema(
					reflect.TypeFor[httpx.Problem](),
					true,
					"Problem",
				),
			},
		},
	}
}

func intPointer(value int) *int {
	return &value
}
