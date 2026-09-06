package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAccessManagementCreatesRoleAndUserAndEnforcesAuthorization(t *testing.T) {
	_, _, server, administratorCookie := newFileHandlerTestApp(t)

	role := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Product viewer",
		[]RoleGrantInput{{PermissionCode: "products:read", Scope: "all"}},
	)
	if role.SystemManaged || len(role.Grants) != 1 ||
		role.Grants[0].Permission.Code != "products:read" ||
		role.Grants[0].Scope != "all" {
		t.Fatalf("created role = %#v", role)
	}

	const (
		userEmail    = "product-viewer@example.com"
		userPassword = "product viewer password value"
	)
	user := createAccessManagementUser(
		t,
		server,
		administratorCookie,
		userEmail,
		userPassword,
		[]string{role.ID},
	)
	if user.Email != userEmail || user.Status != "active" || user.Administrator ||
		len(user.Roles) != 1 || user.Roles[0].ID != role.ID ||
		len(user.Grants) != 1 || user.Grants[0].Permission != "products:read" ||
		user.Grants[0].Scope != "all" {
		t.Fatalf("created user = %#v", user)
	}

	userCookie := loginCookieAs(t, server, userEmail, userPassword)
	forbidden := accessManagementRequest(
		t,
		server,
		userCookie,
		http.MethodPost,
		"/api/v1/roles",
		CreateRoleRequest{Name: "Unauthorized role", Grants: []RoleGrantInput{}},
	)
	assertProblemCode(t, forbidden, http.StatusForbidden, "RESOURCE_FORBIDDEN")

	inUse := accessManagementRequest(
		t,
		server,
		administratorCookie,
		http.MethodDelete,
		"/api/v1/roles/"+role.ID,
		nil,
	)
	assertProblemCode(t, inUse, http.StatusConflict, "ACCESS_ROLE_IN_USE")
}

func TestAccessManagementProtectsAdministratorRoleAndGrants(t *testing.T) {
	_, _, server, administratorCookie := newFileHandlerTestApp(t)
	administrator := findAccessManagementAdministratorRole(t, server, administratorCookie)
	assertAccessManagementAdministratorGrants(t, administrator)

	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{
			name: "update metadata", method: http.MethodPut,
			path: "/api/v1/roles/" + administrator.ID,
			body: UpdateRoleRequest{Name: "Renamed administrator", Description: "must remain unchanged"},
		},
		{
			name: "replace grants", method: http.MethodPut,
			path: "/api/v1/roles/" + administrator.ID + "/grants",
			body: ReplaceRoleGrantsRequest{Grants: []RoleGrantInput{}},
		},
		{
			name: "delete", method: http.MethodDelete,
			path: "/api/v1/roles/" + administrator.ID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := accessManagementRequest(
				t,
				server,
				administratorCookie,
				test.method,
				test.path,
				test.body,
			)
			assertProblemCode(
				t,
				response,
				http.StatusConflict,
				"ACCESS_SYSTEM_ROLE_PROTECTED",
			)
		})
	}

	unchanged := getAccessManagementRole(t, server, administratorCookie, administrator.ID)
	if unchanged.Name != "Administrator" {
		t.Fatalf("Administrator name = %q", unchanged.Name)
	}
	assertAccessManagementAdministratorGrants(t, unchanged)
}

func TestAccessManagementPreventsSelfAndLastAdministratorLockout(t *testing.T) {
	_, _, server, administratorCookie := newFileHandlerTestApp(t)
	administrator := getAccessManagementCurrentUser(t, server, administratorCookie)

	selfTests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{
			name: "replace own roles", method: http.MethodPut,
			path: "/api/v1/users/" + administrator.ID + "/roles",
			body: ReplaceUserRolesRequest{RoleIDs: []string{}},
		},
		{
			name: "disable self", method: http.MethodPost,
			path: "/api/v1/users/" + administrator.ID + "/disable",
		},
		{
			name: "delete self", method: http.MethodDelete,
			path: "/api/v1/users/" + administrator.ID,
		},
		{
			name: "reset own password", method: http.MethodPut,
			path: "/api/v1/users/" + administrator.ID + "/password",
			body: ResetUserPasswordRequest{Password: "must not replace administrator password"},
		},
		{
			name: "revoke own Administrator", method: http.MethodDelete,
			path: "/api/v1/users/" + administrator.ID + "/administrator",
		},
	}
	for _, test := range selfTests {
		t.Run(test.name, func(t *testing.T) {
			response := accessManagementRequest(
				t,
				server,
				administratorCookie,
				test.method,
				test.path,
				test.body,
			)
			assertProblemCode(t, response, http.StatusConflict, "ACCESS_SELF_LOCKOUT")
		})
	}
	getAccessManagementCurrentUser(t, server, administratorCookie)

	operatorRole := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Administrator safety operator",
		[]RoleGrantInput{
			{PermissionCode: "users:delete", Scope: "all"},
			{PermissionCode: "users:disable", Scope: "all"},
		},
	)
	const (
		operatorEmail    = "administrator-safety@example.com"
		operatorPassword = "administrator safety password value"
	)
	createAccessManagementUser(
		t,
		server,
		administratorCookie,
		operatorEmail,
		operatorPassword,
		[]string{operatorRole.ID},
	)
	operatorCookie := loginCookieAs(t, server, operatorEmail, operatorPassword)

	lastAdministratorTests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name: "disable", method: http.MethodPost,
			path: "/api/v1/users/" + administrator.ID + "/disable",
		},
		{
			name: "delete", method: http.MethodDelete,
			path: "/api/v1/users/" + administrator.ID,
		},
	}
	for _, test := range lastAdministratorTests {
		t.Run("last Administrator "+test.name, func(t *testing.T) {
			response := accessManagementRequest(
				t,
				server,
				operatorCookie,
				test.method,
				test.path,
				nil,
			)
			assertProblemCode(t, response, http.StatusConflict, "ACCESS_ADMINISTRATOR_PROTECTED")
		})
	}
	getAccessManagementCurrentUser(t, server, administratorCookie)
}

func TestAccessManagementRevokesSessionsOnDisableResetAndDelete(t *testing.T) {
	t.Run("disable", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		const (
			email    = "disable-session@example.com"
			password = "disable session password value"
		)
		user := createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			password,
			[]string{},
		)
		cookies := []*http.Cookie{
			loginCookieAs(t, server, email, password),
			loginCookieAs(t, server, email, password),
		}
		response := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodPost,
			"/api/v1/users/"+user.ID+"/disable",
			nil,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("disable status = %d, body = %s", response.Code, response.Body.String())
		}
		disabled := decodeAccessManagementResponse[UserResponse](t, response)
		if disabled.Status != "disabled" {
			t.Fatalf("disabled user status = %q", disabled.Status)
		}
		assertAccessManagementSessionsRevoked(t, server, cookies...)
		assertAccessManagementLoginRejected(t, server, email, password)
	})

	t.Run("reset password", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		const (
			email       = "reset-session@example.com"
			oldPassword = "old reset session password value"
			newPassword = "new reset session password value"
		)
		user := createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			oldPassword,
			[]string{},
		)
		cookies := []*http.Cookie{
			loginCookieAs(t, server, email, oldPassword),
			loginCookieAs(t, server, email, oldPassword),
		}
		response := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodPut,
			"/api/v1/users/"+user.ID+"/password",
			ResetUserPasswordRequest{Password: newPassword},
		)
		if response.Code != http.StatusNoContent {
			t.Fatalf("reset password status = %d, body = %s", response.Code, response.Body.String())
		}
		assertAccessManagementSessionsRevoked(t, server, cookies...)
		assertAccessManagementLoginRejected(t, server, email, oldPassword)
		loginCookieAs(t, server, email, newPassword)
	})

	t.Run("delete", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		const (
			email    = "delete-session@example.com"
			password = "delete session password value"
		)
		user := createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			password,
			[]string{},
		)
		cookies := []*http.Cookie{
			loginCookieAs(t, server, email, password),
			loginCookieAs(t, server, email, password),
		}
		response := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodDelete,
			"/api/v1/users/"+user.ID,
			nil,
		)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
		}
		assertAccessManagementSessionsRevoked(t, server, cookies...)
		assertAccessManagementLoginRejected(t, server, email, password)
	})
}

func TestAccessManagementEnforcesDelegationCeiling(t *testing.T) {
	_, _, server, administratorCookie := newFileHandlerTestApp(t)
	delegatorRole := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Limited role delegator",
		[]RoleGrantInput{
			{PermissionCode: "files:read", Scope: "own"},
			{PermissionCode: "roles:create", Scope: "all"},
			{PermissionCode: "roles:grant", Scope: "all"},
		},
	)
	const (
		delegatorEmail    = "limited-delegator@example.com"
		delegatorPassword = "limited delegator password value"
	)
	createAccessManagementUser(
		t,
		server,
		administratorCookie,
		delegatorEmail,
		delegatorPassword,
		[]string{delegatorRole.ID},
	)
	delegatorCookie := loginCookieAs(t, server, delegatorEmail, delegatorPassword)

	escalation := accessManagementRequest(
		t,
		server,
		delegatorCookie,
		http.MethodPost,
		"/api/v1/roles",
		CreateRoleRequest{
			Name: "Escalated file reader",
			Grants: []RoleGrantInput{{
				PermissionCode: "files:read",
				Scope:          "all",
			}},
		},
	)
	assertProblemCode(
		t,
		escalation,
		http.StatusForbidden,
		"ACCESS_DELEGATION_FORBIDDEN",
	)

	roles := listAccessManagementRoles(
		t,
		server,
		administratorCookie,
		"/api/v1/roles?search=Escalated+file+reader&pageSize=100",
	)
	if roles.Total != 0 || len(roles.Items) != 0 {
		t.Fatalf("delegation failure created roles: %#v", roles)
	}
}

func createAccessManagementRole(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	name string,
	grants []RoleGrantInput,
) RoleResponse {
	t.Helper()
	response := accessManagementRequest(
		t,
		server,
		cookie,
		http.MethodPost,
		"/api/v1/roles",
		CreateRoleRequest{Name: name, Description: name + " test role", Grants: grants},
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create role status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeAccessManagementResponse[RoleResponse](t, response)
}

func createAccessManagementUser(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	email string,
	password string,
	roleIDs []string,
) UserResponse {
	t.Helper()
	response := accessManagementRequest(
		t,
		server,
		cookie,
		http.MethodPost,
		"/api/v1/users",
		CreateUserRequest{
			Email:       email,
			DisplayName: email,
			Password:    password,
			RoleIDs:     roleIDs,
		},
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeAccessManagementResponse[UserResponse](t, response)
}

func getAccessManagementCurrentUser(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
) UserResponse {
	t.Helper()
	response := serveRequest(
		server,
		cookie,
		http.MethodGet,
		"/api/v1/auth/me",
		nil,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("current user status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeAccessManagementResponse[UserResponse](t, response)
}

func findAccessManagementAdministratorRole(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
) RoleResponse {
	t.Helper()
	roles := listAccessManagementRoles(
		t,
		server,
		cookie,
		"/api/v1/roles?search=Administrator&pageSize=100",
	)
	for _, role := range roles.Items {
		if role.Name == "Administrator" && role.SystemManaged {
			return role
		}
	}
	t.Fatalf("Administrator role is absent: %#v", roles.Items)
	return RoleResponse{}
}

func getAccessManagementRole(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	roleID string,
) RoleResponse {
	t.Helper()
	response := serveRequest(
		server,
		cookie,
		http.MethodGet,
		"/api/v1/roles/"+roleID,
		nil,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get role status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeAccessManagementResponse[RoleResponse](t, response)
}

func listAccessManagementRoles(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	path string,
) Page[RoleResponse] {
	t.Helper()
	response := serveRequest(server, cookie, http.MethodGet, path, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list roles status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeAccessManagementResponse[Page[RoleResponse]](t, response)
}

func assertAccessManagementAdministratorGrants(t *testing.T, role RoleResponse) {
	t.Helper()
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]struct{}, len(registry.Permissions()))
	for _, permission := range registry.Permissions() {
		want[permission.Code] = struct{}{}
	}
	got := make(map[string]struct{}, len(role.Grants))
	for _, grant := range role.Grants {
		if grant.Scope != "all" {
			t.Errorf("Administrator grant %q scope = %q, want all", grant.Permission.Code, grant.Scope)
		}
		got[grant.Permission.Code] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Administrator grants = %v, want registry permissions %v", got, want)
	}
}

func assertAccessManagementSessionsRevoked(
	t *testing.T,
	server *App,
	cookies ...*http.Cookie,
) {
	t.Helper()
	for index, cookie := range cookies {
		response := serveRequest(
			server,
			cookie,
			http.MethodGet,
			"/api/v1/auth/me",
			nil,
			"",
		)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf(
				"revoked session %d status = %d, want 401; body = %s",
				index,
				response.Code,
				response.Body.String(),
			)
		}
		assertProblemCode(t, response, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	}
}

func assertAccessManagementLoginRejected(
	t *testing.T,
	server *App,
	email string,
	password string,
) {
	t.Helper()
	response := accessManagementRequest(
		t,
		server,
		nil,
		http.MethodPost,
		"/api/v1/auth/login",
		LoginRequest{Email: email, Password: password},
	)
	assertProblemCode(t, response, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
}

func accessManagementRequest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var (
		payload     []byte
		contentType string
	)
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		contentType = jsonMediaType
	}
	return serveRequest(server, cookie, method, path, payload, contentType)
}

func decodeAccessManagementResponse[T any](
	t *testing.T,
	response *httptest.ResponseRecorder,
) T {
	t.Helper()
	var decoded T
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
