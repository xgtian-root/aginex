package app

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/password"
	"gorm.io/gorm"
)

func accessResourceID(c *gin.Context) (string, bool) {
	value := strings.TrimSpace(c.Param("id"))
	if _, err := uuid.Parse(value); err != nil {
		httpx.WriteProblem(
			c,
			http.StatusBadRequest,
			"REQUEST_INVALID",
			"Invalid resource identifier",
			"The path identifier must be a UUID.",
		)
		return "", false
	}
	return value, true
}

func writeAccessFailure(c *gin.Context, operation string, err error) {
	var problem *accessProblemError
	if errors.As(err, &problem) {
		httpx.WriteProblem(
			c,
			problem.Status,
			problem.Code,
			problem.Title,
			problem.Detail,
		)
		return
	}
	if accessNotFound(err) {
		httpx.WriteProblem(
			c,
			http.StatusNotFound,
			"RESOURCE_NOT_FOUND",
			"Access-management resource not found",
			"No resource matches this identifier.",
		)
		return
	}
	logRequestFailure(c, operation, err)
	writeProblem(
		c,
		http.StatusInternalServerError,
		"Access management unavailable",
		"The access-management operation could not be completed.",
	)
}

func userAccessAuditFields(user UserResponse) map[string]any {
	roleIDs := make([]string, 0, len(user.Roles))
	for _, role := range user.Roles {
		roleIDs = append(roleIDs, role.ID)
	}
	return map[string]any{
		"id":            user.ID,
		"displayName":   user.DisplayName,
		"status":        user.Status,
		"roleIds":       roleIDs,
		"administrator": user.Administrator,
	}
}

func roleAccessAuditFields(role RoleResponse) map[string]any {
	grants := make([]map[string]any, 0, len(role.Grants))
	for _, grant := range role.Grants {
		grants = append(grants, map[string]any{
			"permission": grant.Permission.Code,
			"scope":      grant.Scope,
		})
	}
	return map[string]any{
		"id":            role.ID,
		"name":          role.Name,
		"description":   role.Description,
		"systemManaged": role.SystemManaged,
		"grants":        grants,
	}
}

func (a *App) listUsers(c *gin.Context) {
	page, pageSize := pagination(c)
	query := a.db.WithContext(c.Request.Context()).Model(&domain.User{})
	if search := strings.ToLower(strings.TrimSpace(c.Query("search"))); search != "" {
		like := "%" + search + "%"
		query = query.Where(
			"LOWER(email) LIKE ? OR LOWER(display_name) LIKE ?",
			like,
			like,
		)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if status != "active" && status != "disabled" {
			writeAccessFailure(c, "list_users", invalidAccessRequest("status must be active or disabled."))
			return
		}
		query = query.Where("status = ?", status)
	}
	if roleID := strings.TrimSpace(c.Query("roleId")); roleID != "" {
		if _, err := uuid.Parse(roleID); err != nil {
			writeAccessFailure(c, "list_users", invalidAccessRequest("roleId must be a UUID."))
			return
		}
		query = query.Where(
			"EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = users.id AND ur.role_id = ?)",
			roleID,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		writeAccessFailure(c, "list_users_count", err)
		return
	}
	var users []domain.User
	if err := query.Preload("Roles", func(db *gorm.DB) *gorm.DB {
		return db.Order("name ASC")
	}).Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error; err != nil {
		writeAccessFailure(c, "list_users_query", err)
		return
	}
	items := make([]UserListResponse, 0, len(users))
	for _, user := range users {
		roles, administrator := roleSummaries(user.Roles)
		items = append(items, UserListResponse{
			ID:            user.ID,
			Email:         user.Email,
			DisplayName:   user.DisplayName,
			Status:        user.Status,
			Roles:         roles,
			Administrator: administrator,
			CreatedAt:     user.CreatedAt,
			UpdatedAt:     user.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, Page[UserListResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) getUser(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	response, err := a.userResponseTx(
		a.db.WithContext(c.Request.Context()),
		userID,
	)
	if err != nil {
		writeAccessFailure(c, "get_user", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) listRoles(c *gin.Context) {
	page, pageSize := pagination(c)
	query := a.db.WithContext(c.Request.Context()).Model(&domain.Role{})
	if search := strings.ToLower(strings.TrimSpace(c.Query("search"))); search != "" {
		like := "%" + search + "%"
		query = query.Where(
			"LOWER(name) LIKE ? OR LOWER(description) LIKE ?",
			like,
			like,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		writeAccessFailure(c, "list_roles_count", err)
		return
	}
	var roles []domain.Role
	if err := query.Order("name ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&roles).Error; err != nil {
		writeAccessFailure(c, "list_roles_query", err)
		return
	}
	items := make([]RoleResponse, 0, len(roles))
	for _, role := range roles {
		response, err := a.roleResponseTx(
			a.db.WithContext(c.Request.Context()),
			role.ID,
		)
		if err != nil {
			writeAccessFailure(c, "list_roles_response", err)
			return
		}
		items = append(items, response)
	}
	c.JSON(http.StatusOK, Page[RoleResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) getRole(c *gin.Context) {
	roleID, ok := accessResourceID(c)
	if !ok {
		return
	}
	response, err := a.roleResponseTx(
		a.db.WithContext(c.Request.Context()),
		roleID,
	)
	if err != nil {
		writeAccessFailure(c, "get_role", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) listPermissions(c *gin.Context) {
	page, pageSize := pagination(c)
	definitions := a.permissionScopeCatalog()
	codes := make([]string, 0, len(definitions))
	for code := range definitions {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		c.JSON(http.StatusOK, Page[PermissionResponse]{
			Items: []PermissionResponse{}, Page: page, PageSize: pageSize, Total: 0,
		})
		return
	}
	query := a.db.WithContext(c.Request.Context()).Model(&domain.Permission{}).
		Where("code IN ?", codes)
	if search := strings.ToLower(strings.TrimSpace(c.Query("search"))); search != "" {
		like := "%" + search + "%"
		query = query.Where(
			"LOWER(code) LIKE ? OR LOWER(description) LIKE ?",
			like,
			like,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		writeAccessFailure(c, "list_permissions_count", err)
		return
	}
	var permissions []domain.Permission
	if err := query.Order("code ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&permissions).Error; err != nil {
		writeAccessFailure(c, "list_permissions_query", err)
		return
	}
	items := make([]PermissionResponse, 0, len(permissions))
	for _, permission := range permissions {
		items = append(items, a.permissionResponse(permission))
	}
	c.JSON(http.StatusOK, Page[PermissionResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) createUser(c *gin.Context) {
	input, ok := validatedRequestDTO[CreateUserRequest](c)
	if !ok {
		return
	}
	if httpx.UserPasswordLooksInsecure(input.Password) {
		writeAccessFailure(c, "create_user", invalidAccessRequest("password is empty, padded, or resembles a placeholder."))
		return
	}
	passwordHash, err := password.Hash(input.Password)
	if err != nil {
		writeAccessFailure(c, "create_user_hash", err)
		return
	}
	principal := currentPrincipal(c)
	var response UserResponse
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		response, writeErr = a.createUserTx(tx, principal, input, passwordHash)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusCreated,
			response,
			http.Header{"Location": {"/api/v1/users/" + response.ID}},
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"users:create",
			"user",
			response.ID,
			"Created user "+response.DisplayName,
			nil,
			userAccessAuditFields(response),
		), nil
	})
	if err != nil {
		writeAccessFailure(c, "create_user", err)
		return
	}
	c.Header("Location", "/api/v1/users/"+response.ID)
	c.JSON(http.StatusCreated, response)
}

func (a *App) updateUser(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[UpdateUserRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response UserResponse
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, writeErr = a.updateUserTx(tx, principal, userID, input)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "users:update", "user", userID,
			"Updated user "+response.DisplayName,
			userAccessAuditFields(before), userAccessAuditFields(response)), nil
	})
	if err != nil {
		writeAccessFailure(c, "update_user", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) replaceUserRoles(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[ReplaceUserRolesRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response UserResponse
	var revokedSessions int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, revokedSessions, writeErr = a.replaceUserRolesTx(tx, principal, userID, input.RoleIDs)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		after := userAccessAuditFields(response)
		after["sessionCount"] = revokedSessions
		return successfulAuditEvent(c, &principal.User.ID, "users:assign-roles", "user", userID,
			"Replaced user role assignments",
			userAccessAuditFields(before), after), nil
	})
	if err != nil {
		writeAccessFailure(c, "replace_user_roles", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) enableUser(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response UserResponse
	var revokedSessions int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, revokedSessions, writeErr = a.enableUserTx(tx, principal, userID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		after := userAccessAuditFields(response)
		after["sessionCount"] = revokedSessions
		return successfulAuditEvent(c, &principal.User.ID, "users:enable", "user", userID,
			"Enabled user "+response.DisplayName,
			userAccessAuditFields(before), after), nil
	})
	if err != nil {
		writeAccessFailure(c, "enable_user", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) disableUser(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response UserResponse
	var revoked int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, revoked, writeErr = a.disableUserTx(tx, principal, userID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		after := userAccessAuditFields(response)
		after["sessionCount"] = revoked
		return successfulAuditEvent(c, &principal.User.ID, "users:disable", "user", userID,
			"Disabled user "+response.DisplayName,
			userAccessAuditFields(before), after), nil
	})
	if err != nil {
		writeAccessFailure(c, "disable_user", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) deleteUser(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before UserResponse
	var revoked int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, revoked, writeErr = a.deleteUserTx(tx, principal, userID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusNoContent, nil, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		beforeFields := userAccessAuditFields(before)
		beforeFields["sessionCount"] = revoked
		return successfulAuditEvent(c, &principal.User.ID, "users:delete", "user", userID,
			"Deleted user "+before.DisplayName, beforeFields, nil), nil
	})
	if err != nil {
		writeAccessFailure(c, "delete_user", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) resetUserPassword(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[ResetUserPasswordRequest](c)
	if !ok {
		return
	}
	if httpx.UserPasswordLooksInsecure(input.Password) {
		writeAccessFailure(c, "reset_user_password", invalidAccessRequest("password is empty, padded, or resembles a placeholder."))
		return
	}
	passwordHash, err := password.Hash(input.Password)
	if err != nil {
		writeAccessFailure(c, "reset_user_password_hash", err)
		return
	}
	principal := currentPrincipal(c)
	var revoked int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		revoked, writeErr = a.resetUserPasswordTx(tx, principal, userID, passwordHash)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusNoContent, nil, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "users:reset-password", "user", userID,
			"Changed user sign-in access",
			nil, map[string]any{
				"loginAccessChanged": true,
				"sessionCount":       revoked,
			}), nil
	})
	if err != nil {
		writeAccessFailure(c, "reset_user_password", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) grantUserAdministrator(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var response UserResponse
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		response, writeErr = a.grantUserAdministratorTx(tx, principal, userID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "users:grant-administrator", "user", userID,
			"Granted Administrator access to "+response.DisplayName,
			nil, userAccessAuditFields(response)), nil
	})
	if err != nil {
		writeAccessFailure(c, "grant_user_administrator", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) revokeUserAdministrator(c *gin.Context) {
	userID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response UserResponse
	var revokedSessions int64
	a.administratorMu.Lock()
	defer a.administratorMu.Unlock()
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, revokedSessions, writeErr = a.revokeUserAdministratorTx(tx, principal, userID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		after := userAccessAuditFields(response)
		after["sessionCount"] = revokedSessions
		return successfulAuditEvent(c, &principal.User.ID, "users:revoke-administrator", "user", userID,
			"Revoked Administrator access from "+response.DisplayName,
			userAccessAuditFields(before), after), nil
	})
	if err != nil {
		writeAccessFailure(c, "revoke_user_administrator", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) createRole(c *gin.Context) {
	input, ok := validatedRequestDTO[CreateRoleRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var response RoleResponse
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		response, writeErr = a.createRoleTx(tx, principal, input)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(
			c, tx, http.StatusCreated, response,
			http.Header{"Location": {"/api/v1/roles/" + response.ID}},
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "roles:create", "role", response.ID,
			"Created role "+response.Name, nil, roleAccessAuditFields(response)), nil
	})
	if err != nil {
		writeAccessFailure(c, "create_role", err)
		return
	}
	c.Header("Location", "/api/v1/roles/"+response.ID)
	c.JSON(http.StatusCreated, response)
}

func (a *App) updateRole(c *gin.Context) {
	roleID, ok := accessResourceID(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[UpdateRoleRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response RoleResponse
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, writeErr = a.updateRoleTx(tx, principal, roleID, input)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "roles:update", "role", roleID,
			"Updated role "+response.Name,
			roleAccessAuditFields(before), roleAccessAuditFields(response)), nil
	})
	if err != nil {
		writeAccessFailure(c, "update_role", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) replaceRoleGrants(c *gin.Context) {
	roleID, ok := accessResourceID(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[ReplaceRoleGrantsRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before, response RoleResponse
	var revokedSessions int64
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, response, revokedSessions, writeErr = a.replaceRoleGrantsTx(tx, principal, roleID, input.Grants)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		after := roleAccessAuditFields(response)
		after["sessionCount"] = revokedSessions
		return successfulAuditEvent(c, &principal.User.ID, "roles:grant", "role", roleID,
			"Replaced grants for role "+response.Name,
			roleAccessAuditFields(before), after), nil
	})
	if err != nil {
		writeAccessFailure(c, "replace_role_grants", err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) deleteRole(c *gin.Context) {
	roleID, ok := accessResourceID(c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	var before RoleResponse
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var writeErr error
		before, writeErr = a.deleteRoleTx(tx, principal, roleID)
		if writeErr != nil {
			return frameworkaudit.Event{}, writeErr
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusNoContent, nil, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "roles:delete", "role", roleID,
			"Deleted role "+before.Name, roleAccessAuditFields(before), nil), nil
	})
	if err != nil {
		writeAccessFailure(c, "delete_role", err)
		return
	}
	c.Status(http.StatusNoContent)
}
