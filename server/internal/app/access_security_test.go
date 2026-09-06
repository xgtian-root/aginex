package app

import (
	"net/http"
	"testing"

	"github.com/xgtian-root/aginex/server/internal/domain"
)

func TestAccessReductionRevokesAffectedBrowserSessions(t *testing.T) {
	t.Run("user role replacement", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		role := createAccessManagementRole(
			t,
			server,
			administratorCookie,
			"Session reduction role",
			[]RoleGrantInput{{PermissionCode: "products:read", Scope: "all"}},
		)
		const (
			email    = "role-reduction-session@example.com"
			password = "role reduction session password value"
		)
		user := createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			password,
			[]string{role.ID},
		)
		cookie := loginCookieAs(t, server, email, password)

		response := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodPut,
			"/api/v1/users/"+user.ID+"/roles",
			ReplaceUserRolesRequest{RoleIDs: []string{}},
		)
		if response.Code != http.StatusOK {
			t.Fatalf("replace roles status = %d, body = %s", response.Code, response.Body.String())
		}
		assertAccessManagementSessionsRevoked(t, server, cookie)
	})

	t.Run("Administrator revocation", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		const (
			email    = "administrator-revocation-session@example.com"
			password = "administrator revocation session password value"
		)
		user := createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			password,
			[]string{},
		)
		grant := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodPut,
			"/api/v1/users/"+user.ID+"/administrator",
			nil,
		)
		if grant.Code != http.StatusOK {
			t.Fatalf("grant Administrator status = %d, body = %s", grant.Code, grant.Body.String())
		}
		cookie := loginCookieAs(t, server, email, password)

		revoke := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodDelete,
			"/api/v1/users/"+user.ID+"/administrator",
			nil,
		)
		if revoke.Code != http.StatusOK {
			t.Fatalf("revoke Administrator status = %d, body = %s", revoke.Code, revoke.Body.String())
		}
		assertAccessManagementSessionsRevoked(t, server, cookie)
	})

	t.Run("role grant reduction", func(t *testing.T) {
		_, _, server, administratorCookie := newFileHandlerTestApp(t)
		role := createAccessManagementRole(
			t,
			server,
			administratorCookie,
			"Grant reduction role",
			[]RoleGrantInput{{PermissionCode: "products:read", Scope: "all"}},
		)
		const (
			email    = "grant-reduction-session@example.com"
			password = "grant reduction session password value"
		)
		createAccessManagementUser(
			t,
			server,
			administratorCookie,
			email,
			password,
			[]string{role.ID},
		)
		cookie := loginCookieAs(t, server, email, password)

		response := accessManagementRequest(
			t,
			server,
			administratorCookie,
			http.MethodPut,
			"/api/v1/roles/"+role.ID+"/grants",
			ReplaceRoleGrantsRequest{Grants: []RoleGrantInput{}},
		)
		if response.Code != http.StatusOK {
			t.Fatalf("replace grants status = %d, body = %s", response.Code, response.Body.String())
		}
		assertAccessManagementSessionsRevoked(t, server, cookie)
	})
}

func TestRoleManagementPreventsDelegationAndPostStateSelfLockout(t *testing.T) {
	_, _, server, administratorCookie := newFileHandlerTestApp(t)
	operatorRole := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Self-lockout operator",
		[]RoleGrantInput{
			{PermissionCode: "roles:grant", Scope: "all"},
			{PermissionCode: "roles:read", Scope: "all"},
		},
	)
	const (
		operatorEmail    = "self-lockout-operator@example.com"
		operatorPassword = "self lockout operator password value"
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

	lockout := accessManagementRequest(
		t,
		server,
		operatorCookie,
		http.MethodPut,
		"/api/v1/roles/"+operatorRole.ID+"/grants",
		ReplaceRoleGrantsRequest{Grants: []RoleGrantInput{{
			PermissionCode: "roles:read",
			Scope:          "all",
		}}},
	)
	assertProblemCode(t, lockout, http.StatusConflict, "ACCESS_SELF_LOCKOUT")
	unchanged := getAccessManagementRole(t, server, operatorCookie, operatorRole.ID)
	if len(unchanged.Grants) != 2 {
		t.Fatalf("self-lockout rollback grants = %#v", unchanged.Grants)
	}

	privilegedRole := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Above-ceiling unused role",
		[]RoleGrantInput{{PermissionCode: "files:read", Scope: "all"}},
	)
	deleterRole := createAccessManagementRole(
		t,
		server,
		administratorCookie,
		"Limited role deleter",
		[]RoleGrantInput{{PermissionCode: "roles:delete", Scope: "all"}},
	)
	const (
		deleterEmail    = "limited-role-deleter@example.com"
		deleterPassword = "limited role deleter password value"
	)
	createAccessManagementUser(
		t,
		server,
		administratorCookie,
		deleterEmail,
		deleterPassword,
		[]string{deleterRole.ID},
	)
	deleterCookie := loginCookieAs(t, server, deleterEmail, deleterPassword)
	deletion := accessManagementRequest(
		t,
		server,
		deleterCookie,
		http.MethodDelete,
		"/api/v1/roles/"+privilegedRole.ID,
		nil,
	)
	assertProblemCode(t, deletion, http.StatusForbidden, "ACCESS_DELEGATION_FORBIDDEN")
}

func TestEnableRemovesPreexistingDisabledUserSessions(t *testing.T) {
	_, db, server, administratorCookie := newFileHandlerTestApp(t)
	const (
		email    = "enable-stale-session@example.com"
		password = "enable stale session password value"
	)
	user := createAccessManagementUser(
		t,
		server,
		administratorCookie,
		email,
		password,
		[]string{},
	)
	cookie := loginCookieAs(t, server, email, password)
	var stale domain.Session
	if err := db.First(&stale, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	disable := accessManagementRequest(
		t,
		server,
		administratorCookie,
		http.MethodPost,
		"/api/v1/users/"+user.ID+"/disable",
		nil,
	)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", disable.Code, disable.Body.String())
	}
	// Simulate a stale row left by a pre-locking release or an external repair.
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	enable := accessManagementRequest(
		t,
		server,
		administratorCookie,
		http.MethodPost,
		"/api/v1/users/"+user.ID+"/enable",
		nil,
	)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", enable.Code, enable.Body.String())
	}
	assertAccessManagementSessionsRevoked(t, server, cookie)
}
