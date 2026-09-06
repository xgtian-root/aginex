package app

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/internal/auth"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/password"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const administratorRoleName = "Administrator"

type accessProblemError struct {
	Status int
	Code   string
	Title  string
	Detail string
}

func (err *accessProblemError) Error() string {
	return err.Code
}

func newAccessProblem(
	status int,
	code string,
	title string,
	detail string,
) error {
	return &accessProblemError{
		Status: status,
		Code:   code,
		Title:  title,
		Detail: detail,
	}
}

func invalidAccessRequest(detail string) error {
	return newAccessProblem(
		http.StatusBadRequest,
		"REQUEST_INVALID",
		"Invalid access-management request",
		detail,
	)
}

func accessConflict(code string, title string, detail string) error {
	return newAccessProblem(http.StatusConflict, code, title, detail)
}

func accessForbidden(code string, detail string) error {
	return newAccessProblem(
		http.StatusForbidden,
		code,
		"Access-management operation forbidden",
		detail,
	)
}

func requirePrincipalAll(principal auth.Principal, permission string) error {
	scope, ok := principal.Scope(permission)
	if !ok || scope != frameworkauthz.ScopeAll {
		return accessForbidden(
			"RESOURCE_FORBIDDEN",
			"The authenticated user does not hold the required all-scope permission.",
		)
	}
	return nil
}

func requireDelegableGrant(
	principal auth.Principal,
	permission string,
	scope frameworkauthz.GrantScope,
) error {
	if canDelegateGrant(principal, permission, scope) {
		return nil
	}
	return accessForbidden(
		"ACCESS_DELEGATION_FORBIDDEN",
		"A permission grant cannot exceed the authenticated user's effective grant.",
	)
}

func canDelegateGrant(
	principal auth.Principal,
	permission string,
	scope frameworkauthz.GrantScope,
) bool {
	actorScope, ok := principal.Scope(permission)
	return ok && (scope != frameworkauthz.ScopeAll || actorScope == frameworkauthz.ScopeAll)
}

type userRoleRow struct {
	UserID string `gorm:"column:user_id"`
	RoleID string `gorm:"column:role_id"`
}

func (userRoleRow) TableName() string {
	return "user_roles"
}

type permissionGrantRow struct {
	ID          string    `gorm:"column:id"`
	Code        string    `gorm:"column:code"`
	Description string    `gorm:"column:description"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	Scope       string    `gorm:"column:scope"`
}

func normalizeAccessIDs(values []string, field string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, err := uuid.Parse(value); err != nil {
			return nil, invalidAccessRequest(field + " contains an invalid identifier.")
		}
		if _, exists := seen[value]; exists {
			return nil, invalidAccessRequest(field + " contains a duplicate identifier.")
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func isSystemRole(role domain.Role) bool {
	return role.Name == administratorRoleName
}

func roleSummaries(roles []domain.Role) ([]RoleSummaryResponse, bool) {
	result := make([]RoleSummaryResponse, 0, len(roles))
	administrator := false
	for _, role := range roles {
		systemManaged := isSystemRole(role)
		administrator = administrator || systemManaged
		result = append(result, RoleSummaryResponse{
			ID:            role.ID,
			Name:          role.Name,
			SystemManaged: systemManaged,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, administrator
}

func (a *App) permissionScopeCatalog() map[string][]string {
	catalog := make(map[string][]string)
	ownerPermissions := make(map[string]bool)
	for _, definition := range a.registry.Permissions() {
		catalog[definition.Code] = []string{string(frameworkauthz.ScopeAll)}
	}
	for _, operation := range a.registry.Operations() {
		if operation.Policy == policyOwner {
			ownerPermissions[operation.Permission] = true
		}
	}
	for code := range ownerPermissions {
		if _, exists := catalog[code]; exists {
			catalog[code] = []string{
				string(frameworkauthz.ScopeOwn),
				string(frameworkauthz.ScopeAll),
			}
		}
	}
	return catalog
}

func scopeAllowed(allowed []string, scope string) bool {
	for _, candidate := range allowed {
		if candidate == scope {
			return true
		}
	}
	return false
}

func (a *App) permissionResponse(permission domain.Permission) PermissionResponse {
	allowed := a.permissionScopeCatalog()[permission.Code]
	if allowed == nil {
		allowed = []string{}
	}
	return PermissionResponse{
		ID:            permission.ID,
		Code:          permission.Code,
		Description:   permission.Description,
		AllowedScopes: allowed,
		CreatedAt:     permission.CreatedAt,
	}
}

func (a *App) userResponseTx(tx *gorm.DB, userID string) (UserResponse, error) {
	var user domain.User
	if err := tx.Preload("Roles", func(db *gorm.DB) *gorm.DB {
		return db.Order("name ASC")
	}).First(&user, "id = ?", userID).Error; err != nil {
		return UserResponse{}, err
	}
	grants, err := effectiveUserGrantsTx(tx, user.ID)
	if err != nil {
		return UserResponse{}, err
	}
	roles, administrator := roleSummaries(user.Roles)
	permissions := make([]string, 0, len(grants))
	for _, grant := range grants {
		permissions = append(permissions, grant.Permission)
	}
	return UserResponse{
		ID:            user.ID,
		Email:         user.Email,
		DisplayName:   user.DisplayName,
		Status:        user.Status,
		Roles:         roles,
		Administrator: administrator,
		Permissions:   permissions,
		Grants:        grants,
		CreatedAt:     user.CreatedAt,
		UpdatedAt:     user.UpdatedAt,
	}, nil
}

func effectiveUserGrantsTx(tx *gorm.DB, userID string) ([]UserGrantResponse, error) {
	var rows []struct {
		Permission string `gorm:"column:permission"`
		Scope      string `gorm:"column:scope"`
	}
	if err := tx.Table("permissions AS p").
		Select("p.code AS permission, rp.scope AS scope").
		Joins("JOIN role_permissions AS rp ON rp.permission_id = p.id").
		Joins("JOIN user_roles AS ur ON ur.role_id = rp.role_id").
		Where("ur.user_id = ?", userID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Scope != string(frameworkauthz.ScopeOwn) &&
			row.Scope != string(frameworkauthz.ScopeAll) {
			return nil, fmt.Errorf("permission %s has invalid scope %q", row.Permission, row.Scope)
		}
		if merged[row.Permission] == string(frameworkauthz.ScopeAll) {
			continue
		}
		merged[row.Permission] = row.Scope
	}
	codes := make([]string, 0, len(merged))
	for code := range merged {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	result := make([]UserGrantResponse, 0, len(codes))
	for _, code := range codes {
		result = append(result, UserGrantResponse{
			Permission: code,
			Scope:      merged[code],
		})
	}
	return result, nil
}

func userAccessReduced(before UserResponse, after UserResponse) bool {
	previous := make(map[string]string, len(before.Grants))
	current := make(map[string]string, len(after.Grants))
	for _, grant := range before.Grants {
		previous[grant.Permission] = grant.Scope
	}
	for _, grant := range after.Grants {
		current[grant.Permission] = grant.Scope
	}
	return grantMapReduced(previous, current)
}

func userRoleAssignmentsChanged(before UserResponse, after UserResponse) bool {
	if len(before.Roles) != len(after.Roles) {
		return true
	}
	previous := make(map[string]struct{}, len(before.Roles))
	for _, role := range before.Roles {
		previous[role.ID] = struct{}{}
	}
	for _, role := range after.Roles {
		if _, exists := previous[role.ID]; !exists {
			return true
		}
	}
	return false
}

func roleAccessReduced(before RoleResponse, after RoleResponse) bool {
	previous := make(map[string]string, len(before.Grants))
	current := make(map[string]string, len(after.Grants))
	for _, grant := range before.Grants {
		previous[grant.Permission.Code] = grant.Scope
	}
	for _, grant := range after.Grants {
		current[grant.Permission.Code] = grant.Scope
	}
	return grantMapReduced(previous, current)
}

func grantMapReduced(previous map[string]string, current map[string]string) bool {
	for code, previousScope := range previous {
		currentScope, exists := current[code]
		if !exists ||
			(previousScope == string(frameworkauthz.ScopeAll) &&
				currentScope != string(frameworkauthz.ScopeAll)) {
			return true
		}
	}
	return false
}

func actorRetainsRoleGrantTx(tx *gorm.DB, actorID string) error {
	grants, err := effectiveUserGrantsTx(tx, actorID)
	if err != nil {
		return err
	}
	for _, grant := range grants {
		if grant.Permission == "roles:grant" &&
			grant.Scope == string(frameworkauthz.ScopeAll) {
			return nil
		}
	}
	return accessConflict(
		"ACCESS_SELF_LOCKOUT",
		"Self role grant lockout is blocked",
		"The change would remove the authenticated user's all-scope role grant permission.",
	)
}

func revokeRoleUserSessionsTx(tx *gorm.DB, roleID string) (int64, error) {
	var userIDs []string
	if err := tx.Table("user_roles").
		Where("role_id = ?", roleID).
		Order("user_id ASC").
		Pluck("user_id", &userIDs).Error; err != nil {
		return 0, err
	}
	for _, userID := range userIDs {
		// Login locks only one identity aggregate. Role writers already hold the
		// role row and take affected users in stable order, so this lock order is
		// deterministic and cannot form a cycle with login.
		if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
			return 0, err
		}
	}
	if len(userIDs) == 0 {
		return 0, nil
	}
	result := tx.Where("user_id IN ?", userIDs).Delete(&domain.Session{})
	return result.RowsAffected, result.Error
}

func (a *App) roleResponseTx(tx *gorm.DB, roleID string) (RoleResponse, error) {
	var role domain.Role
	if err := tx.First(&role, "id = ?", roleID).Error; err != nil {
		return RoleResponse{}, err
	}
	var userCount int64
	if err := tx.Table("user_roles AS ur").
		Joins("JOIN users AS u ON u.id = ur.user_id").
		Where("ur.role_id = ?", role.ID).
		Where("u.deleted_at IS NULL").
		Count(&userCount).Error; err != nil {
		return RoleResponse{}, err
	}
	rows, err := roleGrantRowsTx(tx, role.ID)
	if err != nil {
		return RoleResponse{}, err
	}
	grants := make([]RoleGrantResponse, 0, len(rows))
	for _, row := range rows {
		permission := domain.Permission{
			ID:          row.ID,
			Code:        row.Code,
			Description: row.Description,
			CreatedAt:   row.CreatedAt,
		}
		grants = append(grants, RoleGrantResponse{
			Permission: a.permissionResponse(permission),
			Scope:      row.Scope,
		})
	}
	return RoleResponse{
		ID:            role.ID,
		Name:          role.Name,
		Description:   role.Description,
		SystemManaged: isSystemRole(role),
		UserCount:     userCount,
		Grants:        grants,
		CreatedAt:     role.CreatedAt,
		UpdatedAt:     role.UpdatedAt,
	}, nil
}

func roleGrantRowsTx(tx *gorm.DB, roleID string) ([]permissionGrantRow, error) {
	return roleGrantRowsQueryTx(tx, roleID, false)
}

func roleGrantRowsForUpdateTx(tx *gorm.DB, roleID string) ([]permissionGrantRow, error) {
	return roleGrantRowsQueryTx(tx, roleID, true)
}

func roleGrantRowsQueryTx(
	tx *gorm.DB,
	roleID string,
	forUpdate bool,
) ([]permissionGrantRow, error) {
	var rows []permissionGrantRow
	query := tx.Table("role_permissions AS rp").
		Select("p.id, p.code, p.description, p.created_at, rp.scope").
		Joins("JOIN permissions AS p ON p.id = rp.permission_id").
		Where("rp.role_id = ?", roleID).
		Order("p.code ASC")
	if forUpdate {
		// MySQL's REPEATABLE READ consistent snapshot may have been established
		// by an earlier validation query. A locking read is a current read, so
		// assignment always validates the grants committed at the role-row lock
		// linearization point. PostgreSQL emits the equivalent FOR UPDATE and
		// SQLite safely omits the unsupported clause.
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Scan(&rows).Error
	return rows, err
}

func (a *App) loadAssignableRolesTx(
	tx *gorm.DB,
	principal auth.Principal,
	roleIDs []string,
) ([]domain.Role, error) {
	roleIDs, err := normalizeAccessIDs(roleIDs, "roleIds")
	if err != nil {
		return nil, err
	}
	if len(roleIDs) == 0 {
		return []domain.Role{}, nil
	}
	var existing []domain.Role
	if err := tx.Where("id IN ?", roleIDs).Find(&existing).Error; err != nil {
		return nil, err
	}
	if len(existing) != len(roleIDs) {
		return nil, invalidAccessRequest("roleIds contains an unknown role.")
	}
	for _, role := range existing {
		if isSystemRole(role) {
			return nil, invalidAccessRequest(
				"Administrator must be granted through the dedicated administrator operation.",
			)
		}
	}
	// Every writer takes role locks in stable identifier order. This prevents
	// multi-role assignments from deadlocking each other and linearizes their
	// grant validation with role grant replacement and deletion.
	orderedIDs := append([]string(nil), roleIDs...)
	sort.Strings(orderedIDs)
	roles := make([]domain.Role, 0, len(orderedIDs))
	for _, roleID := range orderedIDs {
		role, lockErr := lockRoleTx(tx, roleID)
		if errors.Is(lockErr, gorm.ErrRecordNotFound) {
			return nil, invalidAccessRequest("roleIds contains an unknown role.")
		}
		if lockErr != nil {
			return nil, lockErr
		}
		if isSystemRole(role) {
			return nil, invalidAccessRequest(
				"Administrator must be granted through the dedicated administrator operation.",
			)
		}
		roles = append(roles, role)
	}
	allowedScopes := a.permissionScopeCatalog()
	for _, role := range roles {
		grants, grantErr := roleGrantRowsForUpdateTx(tx, role.ID)
		if grantErr != nil {
			return nil, grantErr
		}
		for _, grant := range grants {
			if !scopeAllowed(allowedScopes[grant.Code], grant.Scope) {
				return nil, accessConflict(
					"ACCESS_ROLE_GRANTS_INVALID",
					"Role grants are invalid",
					"The role contains a grant scope that is not valid for its permission.",
				)
			}
			if err := requireDelegableGrant(
				principal,
				grant.Code,
				frameworkauthz.GrantScope(grant.Scope),
			); err != nil {
				return nil, err
			}
		}
	}
	return roles, nil
}

func replaceUserRolesTx(tx *gorm.DB, userID string, roles []domain.Role) error {
	// This helper is deliberately incapable of removing Administrator. The
	// dedicated revoke operation owns its stronger permission, usable-actor,
	// serialization, and last-administrator invariants.
	if err := tx.Exec(
		"DELETE FROM user_roles WHERE user_id = ? AND role_id NOT IN (SELECT id FROM roles WHERE name = ?)",
		userID,
		administratorRoleName,
	).Error; err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	rows := make([]userRoleRow, 0, len(roles))
	for _, role := range roles {
		rows = append(rows, userRoleRow{UserID: userID, RoleID: role.ID})
	}
	return tx.Create(&rows).Error
}

func (a *App) createUserTx(
	tx *gorm.DB,
	principal auth.Principal,
	input CreateUserRequest,
	passwordHash string,
) (UserResponse, error) {
	if err := requirePrincipalAll(principal, "users:create"); err != nil {
		return UserResponse{}, err
	}
	if !password.ValidHash(passwordHash) {
		return UserResponse{}, errors.New("local sign-in hash is invalid")
	}
	if len(input.RoleIDs) > 0 {
		if err := requirePrincipalAll(principal, "users:assign-roles"); err != nil {
			return UserResponse{}, err
		}
	}
	email := auth.NormalizePasswordSubject(input.Email)
	if email == "" {
		return UserResponse{}, invalidAccessRequest("email is required.")
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return UserResponse{}, invalidAccessRequest("displayName is required.")
	}
	var duplicateCount int64
	if err := tx.Unscoped().Model(&domain.User{}).
		Where("LOWER(TRIM(email)) = ?", email).
		Count(&duplicateCount).Error; err != nil {
		return UserResponse{}, err
	}
	if duplicateCount > 0 {
		return UserResponse{}, accessConflict(
			"REQUEST_CONFLICT",
			"User already exists",
			"A user with this email already exists.",
		)
	}
	roles, err := a.loadAssignableRolesTx(tx, principal, input.RoleIDs)
	if err != nil {
		return UserResponse{}, err
	}
	now := time.Now().UTC()
	user := domain.User{
		ID:          uuid.NewString(),
		Email:       email,
		DisplayName: displayName,
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Create(&user).Error; err != nil {
		return UserResponse{}, err
	}
	identity := domain.UserIdentity{
		ID:             uuid.NewString(),
		UserID:         user.ID,
		Provider:       domain.IdentityProviderPassword,
		Subject:        email,
		CredentialHash: passwordHash,
		Status:         domain.IdentityStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := tx.Create(&identity).Error; err != nil {
		return UserResponse{}, err
	}
	if err := replaceUserRolesTx(tx, user.ID, roles); err != nil {
		return UserResponse{}, err
	}
	return a.userResponseTx(tx, user.ID)
}

func (a *App) updateUserTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
	input UpdateUserRequest,
) (UserResponse, UserResponse, error) {
	if err := requirePrincipalAll(principal, "users:update"); err != nil {
		return UserResponse{}, UserResponse{}, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return UserResponse{}, UserResponse{}, invalidAccessRequest("displayName is required.")
	}
	if err := tx.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]any{
		"display_name": displayName,
		"updated_at":   time.Now().UTC(),
	}).Error; err != nil {
		return UserResponse{}, UserResponse{}, err
	}
	after, err := a.userResponseTx(tx, userID)
	return before, after, err
}

func (a *App) replaceUserRolesTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
	roleIDs []string,
) (UserResponse, UserResponse, int64, error) {
	if err := requirePrincipalAll(principal, "users:assign-roles"); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if userID == principal.User.ID {
		return UserResponse{}, UserResponse{}, 0, accessConflict(
			"ACCESS_SELF_LOCKOUT",
			"Self role replacement is blocked",
			"An administrator cannot replace their own roles through an administrative endpoint.",
		)
	}
	// Serialize the target classification with explicit Administrator grants.
	// The replacement itself preserves the system join defensively, but it must
	// also reject a target that was already privileged at its linearization
	// point.
	if _, err := a.lockAdministratorRoleTx(tx); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if before.Administrator {
		return UserResponse{}, UserResponse{}, 0, accessConflict(
			"ACCESS_ADMINISTRATOR_PROTECTED",
			"Administrator user is protected",
			"Revoke Administrator access explicitly before replacing this user's roles.",
		)
	}
	roles, err := a.loadAssignableRolesTx(tx, principal, roleIDs)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if err := replaceUserRolesTx(tx, userID, roles); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if err := tx.Model(&domain.User{}).Where("id = ?", userID).
		Update("updated_at", time.Now().UTC()).Error; err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	after, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	var revoked int64
	// Revoke conservatively for every membership change. Besides covering
	// direct reductions, this closes the edge where a newly assigned role was
	// concurrently reduced before the target joined and therefore was not part
	// of that role writer's affected-user set.
	if userRoleAssignmentsChanged(before, after) || userAccessReduced(before, after) {
		if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
		revoked, err = a.auth.RevokeAllSessionsTx(tx, userID)
		if err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	return before, after, revoked, nil
}

func (a *App) enableUserTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
) (UserResponse, UserResponse, int64, error) {
	if err := requirePrincipalAll(principal, "users:enable"); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	administratorRole, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	// Serialize the disabled-to-active transition with password login. This
	// also cleans up sessions created by an older release before disable and
	// login shared an identity lock, preventing stale tokens from reviving.
	if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if before.Administrator {
		if err := ensureUsableAdministratorTx(
			tx,
			administratorRole.ID,
			principal.User.ID,
		); err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	var revoked int64
	if before.Status != "active" {
		revoked, err = a.auth.RevokeAllSessionsTx(tx, userID)
		if err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	if err := tx.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]any{
		"status":     "active",
		"updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	after, err := a.userResponseTx(tx, userID)
	return before, after, revoked, err
}

func (a *App) lockAdministratorRoleTx(tx *gorm.DB) (domain.Role, error) {
	var role domain.Role
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("name = ?", administratorRoleName).
		First(&role).Error; err != nil {
		return domain.Role{}, err
	}
	// SQLite omits FOR UPDATE. A no-op update still acquires its single-writer
	// lock; PostgreSQL and MySQL keep the canonical role row locked as well.
	if err := tx.Model(&domain.Role{}).Where("id = ?", role.ID).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return domain.Role{}, err
	}
	return role, nil
}

func lockRoleTx(tx *gorm.DB, roleID string) (domain.Role, error) {
	var role domain.Role
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&role, "id = ?", roleID).Error; err != nil {
		return domain.Role{}, err
	}
	if err := tx.Model(&domain.Role{}).Where("id = ?", role.ID).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return domain.Role{}, err
	}
	return role, nil
}

func activeAdministratorIDsTx(
	tx *gorm.DB,
	roleID string,
) (map[string]struct{}, error) {
	var rows []struct {
		UserID         string `gorm:"column:user_id"`
		CredentialHash string `gorm:"column:credential_hash"`
	}
	if err := tx.Table("users AS u").
		Select("u.id AS user_id, ui.credential_hash").
		Joins("JOIN user_roles AS ur ON ur.user_id = u.id").
		Joins("JOIN user_identities AS ui ON ui.user_id = u.id").
		Where("ur.role_id = ?", roleID).
		Where("u.deleted_at IS NULL").
		Where("u.status = ?", "active").
		Where("ui.provider = ?", domain.IdentityProviderPassword).
		Where("ui.status = ?", domain.IdentityStatusActive).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]struct{})
	for _, row := range rows {
		if password.ValidHash(row.CredentialHash) {
			result[row.UserID] = struct{}{}
		}
	}
	return result, nil
}

func ensureUsableAdministratorTx(
	tx *gorm.DB,
	roleID string,
	userID string,
) error {
	ids, err := activeAdministratorIDsTx(tx, roleID)
	if err != nil {
		return err
	}
	if _, ok := ids[userID]; !ok {
		return accessForbidden(
			"ACCESS_ADMINISTRATOR_REQUIRED",
			"The operation requires an active, login-capable Administrator.",
		)
	}
	return nil
}

func ensureAdministratorRemainsTx(tx *gorm.DB, roleID string) error {
	ids, err := activeAdministratorIDsTx(tx, roleID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return accessConflict(
			"ACCESS_LAST_ADMINISTRATOR",
			"Last Administrator is protected",
			"At least one active, login-capable Administrator must remain.",
		)
	}
	return nil
}

func (a *App) disableUserTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
) (UserResponse, UserResponse, int64, error) {
	if err := requirePrincipalAll(principal, "users:disable"); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if userID == principal.User.ID {
		return UserResponse{}, UserResponse{}, 0, accessConflict(
			"ACCESS_SELF_LOCKOUT",
			"Self disable is blocked",
			"An administrator cannot disable their own account.",
		)
	}
	administratorRole, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	// A password login holds the same identity rows through session creation.
	// Waiting here makes the subsequent session deletion include every login
	// that authenticated before the disable linearization point.
	if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if before.Administrator {
		return UserResponse{}, UserResponse{}, 0, accessConflict(
			"ACCESS_ADMINISTRATOR_PROTECTED",
			"Administrator user is protected",
			"Revoke Administrator access explicitly before disabling this user.",
		)
	}
	if err := tx.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]any{
		"status":     "disabled",
		"updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	revoked, err := a.auth.RevokeAllSessionsTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if before.Administrator {
		if err := ensureAdministratorRemainsTx(tx, administratorRole.ID); err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	after, err := a.userResponseTx(tx, userID)
	return before, after, revoked, err
}

func (a *App) deleteUserTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
) (UserResponse, int64, error) {
	if err := requirePrincipalAll(principal, "users:delete"); err != nil {
		return UserResponse{}, 0, err
	}
	if userID == principal.User.ID {
		return UserResponse{}, 0, accessConflict(
			"ACCESS_SELF_LOCKOUT",
			"Self deletion is blocked",
			"An administrator cannot delete their own account.",
		)
	}
	administratorRole, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return UserResponse{}, 0, err
	}
	if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
		return UserResponse{}, 0, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, 0, err
	}
	if before.Administrator {
		return UserResponse{}, 0, accessConflict(
			"ACCESS_ADMINISTRATOR_PROTECTED",
			"Administrator user is protected",
			"Revoke Administrator access explicitly before deleting this user.",
		)
	}
	if err := tx.Exec("DELETE FROM user_roles WHERE user_id = ?", userID).Error; err != nil {
		return UserResponse{}, 0, err
	}
	revoked, err := a.auth.RevokeAllSessionsTx(tx, userID)
	if err != nil {
		return UserResponse{}, 0, err
	}
	if err := tx.Delete(&domain.User{}, "id = ?", userID).Error; err != nil {
		return UserResponse{}, 0, err
	}
	if before.Administrator {
		if err := ensureAdministratorRemainsTx(tx, administratorRole.ID); err != nil {
			return UserResponse{}, 0, err
		}
	}
	return before, revoked, nil
}

func (a *App) resetUserPasswordTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
	passwordHash string,
) (int64, error) {
	if err := requirePrincipalAll(principal, "users:reset-password"); err != nil {
		return 0, err
	}
	if !password.ValidHash(passwordHash) {
		return 0, errors.New("local sign-in hash is invalid")
	}
	if userID == principal.User.ID {
		return 0, accessConflict(
			"ACCESS_SELF_LOCKOUT",
			"Self password reset is blocked",
			"Use a self-service password change flow that verifies the current password.",
		)
	}
	// Serialize credential replacement with Administrator grant/revoke. Without
	// this lock, a reset authorized for an ordinary user could race with an
	// Administrator grant and take effect after the target became privileged.
	if _, err := a.lockAdministratorRoleTx(tx); err != nil {
		return 0, err
	}
	var user domain.User
	if err := tx.Preload("Roles").First(&user, "id = ?", userID).Error; err != nil {
		return 0, err
	}
	for _, role := range user.Roles {
		if isSystemRole(role) {
			return 0, accessConflict(
				"ACCESS_ADMINISTRATOR_PROTECTED",
				"Administrator user is protected",
				"Revoke Administrator access explicitly before resetting this user's password.",
			)
		}
	}
	identities, err := auth.LockPasswordIdentitiesTx(tx, user.ID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	switch len(identities) {
	case 0:
		identity := domain.UserIdentity{
			ID:             uuid.NewString(),
			UserID:         user.ID,
			Provider:       domain.IdentityProviderPassword,
			Subject:        auth.NormalizePasswordSubject(user.Email),
			CredentialHash: passwordHash,
			Status:         domain.IdentityStatusActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&identity).Error; err != nil {
			return 0, err
		}
	case 1:
		if err := tx.Model(&domain.UserIdentity{}).
			Where("id = ?", identities[0].ID).
			Updates(map[string]any{
				"credential_hash": passwordHash,
				"subject":         auth.NormalizePasswordSubject(user.Email),
				"status":          domain.IdentityStatusActive,
				"updated_at":      now,
			}).Error; err != nil {
			return 0, err
		}
	default:
		return 0, accessConflict(
			"ACCESS_IDENTITY_CONFLICT",
			"Password identity is inconsistent",
			"The user has more than one local password identity.",
		)
	}
	if err := tx.Model(&domain.User{}).Where("id = ?", user.ID).
		Update("updated_at", now).Error; err != nil {
		return 0, err
	}
	return a.auth.RevokeAllSessionsTx(tx, user.ID)
}

func (a *App) grantUserAdministratorTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
) (UserResponse, error) {
	if err := requirePrincipalAll(principal, "users:grant-administrator"); err != nil {
		return UserResponse{}, err
	}
	role, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return UserResponse{}, err
	}
	if err := ensureUsableAdministratorTx(tx, role.ID, principal.User.ID); err != nil {
		return UserResponse{}, err
	}
	var user domain.User
	if err := tx.First(&user, "id = ?", userID).Error; err != nil {
		return UserResponse{}, err
	}
	if user.Status != "active" {
		return UserResponse{}, accessConflict(
			"REQUEST_CONFLICT",
			"User is not login-capable",
			"Enable the user before granting Administrator access.",
		)
	}
	var hashes []string
	if err := tx.Model(&domain.UserIdentity{}).
		Where("user_id = ? AND provider = ? AND status = ?", user.ID, domain.IdentityProviderPassword, domain.IdentityStatusActive).
		Pluck("credential_hash", &hashes).Error; err != nil {
		return UserResponse{}, err
	}
	loginCapable := false
	for _, hash := range hashes {
		loginCapable = loginCapable || password.ValidHash(hash)
	}
	if !loginCapable {
		return UserResponse{}, accessConflict(
			"REQUEST_CONFLICT",
			"User is not login-capable",
			"The user needs an active local password identity before receiving Administrator access.",
		)
	}
	assignment := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&userRoleRow{
		UserID: user.ID,
		RoleID: role.ID,
	})
	if assignment.Error != nil {
		return UserResponse{}, assignment.Error
	}
	if assignment.RowsAffected > 0 {
		if err := tx.Model(&domain.User{}).Where("id = ?", user.ID).
			Update("updated_at", time.Now().UTC()).Error; err != nil {
			return UserResponse{}, err
		}
	}
	return a.userResponseTx(tx, user.ID)
}

func (a *App) revokeUserAdministratorTx(
	tx *gorm.DB,
	principal auth.Principal,
	userID string,
) (UserResponse, UserResponse, int64, error) {
	if err := requirePrincipalAll(principal, "users:revoke-administrator"); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if userID == principal.User.ID {
		return UserResponse{}, UserResponse{}, 0, accessConflict(
			"ACCESS_SELF_LOCKOUT",
			"Self Administrator revocation is blocked",
			"An administrator cannot revoke their own Administrator access.",
		)
	}
	role, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	if err := ensureUsableAdministratorTx(tx, role.ID, principal.User.ID); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	before, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	revocation := tx.Exec(
		"DELETE FROM user_roles WHERE user_id = ? AND role_id = ?",
		userID,
		role.ID,
	)
	if revocation.Error != nil {
		return UserResponse{}, UserResponse{}, 0, revocation.Error
	}
	if revocation.RowsAffected > 0 {
		if err := tx.Model(&domain.User{}).Where("id = ?", userID).
			Update("updated_at", time.Now().UTC()).Error; err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	if err := ensureAdministratorRemainsTx(tx, role.ID); err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	after, err := a.userResponseTx(tx, userID)
	if err != nil {
		return UserResponse{}, UserResponse{}, 0, err
	}
	var revoked int64
	if revocation.RowsAffected > 0 {
		if _, err := auth.LockPasswordIdentitiesTx(tx, userID); err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
		revoked, err = a.auth.RevokeAllSessionsTx(tx, userID)
		if err != nil {
			return UserResponse{}, UserResponse{}, 0, err
		}
	}
	return before, after, revoked, nil
}

func normalizeRoleName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalidAccessRequest("name is required.")
	}
	if strings.EqualFold(name, administratorRoleName) {
		return "", accessConflict(
			"ACCESS_SYSTEM_ROLE_PROTECTED",
			"Administrator role is protected",
			"The Administrator role name is reserved for the framework-managed role.",
		)
	}
	return name, nil
}

func ensureRoleNameAvailableTx(tx *gorm.DB, name string, excludeID string) error {
	query := tx.Model(&domain.Role{}).Where("LOWER(TRIM(name)) = ?", strings.ToLower(name))
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return accessConflict(
			"REQUEST_CONFLICT",
			"Role already exists",
			"A role with this name already exists.",
		)
	}
	return nil
}

func (a *App) validatedRoleGrantsTx(
	tx *gorm.DB,
	principal auth.Principal,
	inputs []RoleGrantInput,
	existing map[string]string,
) ([]domain.RolePermission, error) {
	definitions := a.permissionScopeCatalog()
	seen := make(map[string]struct{}, len(inputs))
	submittedScopes := make(map[string]string, len(inputs))
	codes := make([]string, 0, len(inputs))
	for _, input := range inputs {
		code := strings.TrimSpace(input.PermissionCode)
		if _, exists := seen[code]; exists {
			return nil, invalidAccessRequest("grants contains a duplicate permissionCode.")
		}
		allowed, exists := definitions[code]
		if !exists {
			return nil, invalidAccessRequest("grants contains an unknown permissionCode.")
		}
		if !scopeAllowed(allowed, input.Scope) {
			return nil, invalidAccessRequest("grants contains a scope that is not allowed for its permission.")
		}
		if existing[code] != input.Scope {
			if err := requireDelegableGrant(
				principal,
				code,
				frameworkauthz.GrantScope(input.Scope),
			); err != nil {
				return nil, err
			}
		}
		seen[code] = struct{}{}
		submittedScopes[code] = input.Scope
		codes = append(codes, code)
	}
	for code, scope := range existing {
		// A permission removed from the compiled registry cannot authorize any
		// current operation and cannot be submitted as a new grant. Allow a full
		// replacement to remove that stale database row while preserving it in
		// the before-audit snapshot.
		if _, registered := definitions[code]; !registered {
			continue
		}
		if canDelegateGrant(
			principal,
			code,
			frameworkauthz.GrantScope(scope),
		) {
			continue
		}
		submittedScope, preserved := submittedScopes[code]
		if !preserved || submittedScope != scope {
			return nil, accessForbidden(
				"ACCESS_DELEGATION_FORBIDDEN",
				"A grant outside the authenticated user's delegation ceiling must be preserved unchanged.",
			)
		}
	}
	if len(codes) == 0 {
		return []domain.RolePermission{}, nil
	}
	var permissions []domain.Permission
	if err := tx.Where("code IN ?", codes).Find(&permissions).Error; err != nil {
		return nil, err
	}
	if len(permissions) != len(codes) {
		return nil, accessConflict(
			"ACCESS_PERMISSION_REGISTRY_DRIFT",
			"Permission registry is unavailable",
			"A registered permission is missing from the database. Run bootstrap before managing grants.",
		)
	}
	byCode := make(map[string]domain.Permission, len(permissions))
	for _, permission := range permissions {
		byCode[permission.Code] = permission
	}
	result := make([]domain.RolePermission, 0, len(inputs))
	for _, input := range inputs {
		permission := byCode[strings.TrimSpace(input.PermissionCode)]
		result = append(result, domain.RolePermission{
			PermissionID: permission.ID,
			Scope:        input.Scope,
		})
	}
	return result, nil
}

func replaceRoleGrantRowsTx(
	tx *gorm.DB,
	roleID string,
	grants []domain.RolePermission,
) error {
	if err := tx.Exec("DELETE FROM role_permissions WHERE role_id = ?", roleID).Error; err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	for index := range grants {
		grants[index].RoleID = roleID
	}
	return tx.Create(&grants).Error
}

func (a *App) createRoleTx(
	tx *gorm.DB,
	principal auth.Principal,
	input CreateRoleRequest,
) (RoleResponse, error) {
	if err := requirePrincipalAll(principal, "roles:create"); err != nil {
		return RoleResponse{}, err
	}
	if len(input.Grants) > 0 {
		if err := requirePrincipalAll(principal, "roles:grant"); err != nil {
			return RoleResponse{}, err
		}
	}
	if _, err := a.lockAdministratorRoleTx(tx); err != nil {
		return RoleResponse{}, err
	}
	name, err := normalizeRoleName(input.Name)
	if err != nil {
		return RoleResponse{}, err
	}
	if err := ensureRoleNameAvailableTx(tx, name, ""); err != nil {
		return RoleResponse{}, err
	}
	grants, err := a.validatedRoleGrantsTx(tx, principal, input.Grants, nil)
	if err != nil {
		return RoleResponse{}, err
	}
	now := time.Now().UTC()
	role := domain.Role{
		ID:          uuid.NewString(),
		Name:        name,
		Description: strings.TrimSpace(input.Description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Create(&role).Error; err != nil {
		return RoleResponse{}, err
	}
	if err := replaceRoleGrantRowsTx(tx, role.ID, grants); err != nil {
		return RoleResponse{}, err
	}
	return a.roleResponseTx(tx, role.ID)
}

func (a *App) updateRoleTx(
	tx *gorm.DB,
	principal auth.Principal,
	roleID string,
	input UpdateRoleRequest,
) (RoleResponse, RoleResponse, error) {
	if err := requirePrincipalAll(principal, "roles:update"); err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	if _, err := a.lockAdministratorRoleTx(tx); err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	if _, err := lockRoleTx(tx, roleID); err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	before, err := a.roleResponseTx(tx, roleID)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	if before.SystemManaged {
		return RoleResponse{}, RoleResponse{}, accessConflict(
			"ACCESS_SYSTEM_ROLE_PROTECTED",
			"Administrator role is protected",
			"Framework-managed role metadata cannot be changed.",
		)
	}
	name, err := normalizeRoleName(input.Name)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	if err := ensureRoleNameAvailableTx(tx, name, roleID); err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	if err := tx.Model(&domain.Role{}).Where("id = ?", roleID).Updates(map[string]any{
		"name":        name,
		"description": strings.TrimSpace(input.Description),
		"updated_at":  time.Now().UTC(),
	}).Error; err != nil {
		return RoleResponse{}, RoleResponse{}, err
	}
	after, err := a.roleResponseTx(tx, roleID)
	return before, after, err
}

func (a *App) replaceRoleGrantsTx(
	tx *gorm.DB,
	principal auth.Principal,
	roleID string,
	inputs []RoleGrantInput,
) (RoleResponse, RoleResponse, int64, error) {
	if err := requirePrincipalAll(principal, "roles:grant"); err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	administratorRole, err := a.lockAdministratorRoleTx(tx)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	if roleID != administratorRole.ID {
		if _, err := lockRoleTx(tx, roleID); err != nil {
			return RoleResponse{}, RoleResponse{}, 0, err
		}
	}
	before, err := a.roleResponseTx(tx, roleID)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	if before.SystemManaged {
		return RoleResponse{}, RoleResponse{}, 0, accessConflict(
			"ACCESS_SYSTEM_ROLE_PROTECTED",
			"Administrator role is protected",
			"Framework-managed role grants cannot be changed.",
		)
	}
	existing := make(map[string]string, len(before.Grants))
	for _, grant := range before.Grants {
		existing[grant.Permission.Code] = grant.Scope
	}
	grants, err := a.validatedRoleGrantsTx(tx, principal, inputs, existing)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	if err := replaceRoleGrantRowsTx(tx, roleID, grants); err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	if err := actorRetainsRoleGrantTx(tx, principal.User.ID); err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	if err := tx.Model(&domain.Role{}).Where("id = ?", roleID).
		Update("updated_at", time.Now().UTC()).Error; err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	after, err := a.roleResponseTx(tx, roleID)
	if err != nil {
		return RoleResponse{}, RoleResponse{}, 0, err
	}
	var revoked int64
	if roleAccessReduced(before, after) {
		revoked, err = revokeRoleUserSessionsTx(tx, roleID)
		if err != nil {
			return RoleResponse{}, RoleResponse{}, 0, err
		}
	}
	return before, after, revoked, nil
}

func (a *App) deleteRoleTx(
	tx *gorm.DB,
	principal auth.Principal,
	roleID string,
) (RoleResponse, error) {
	if err := requirePrincipalAll(principal, "roles:delete"); err != nil {
		return RoleResponse{}, err
	}
	if _, err := lockRoleTx(tx, roleID); err != nil {
		return RoleResponse{}, err
	}
	role, err := a.roleResponseTx(tx, roleID)
	if err != nil {
		return RoleResponse{}, err
	}
	if role.SystemManaged {
		return RoleResponse{}, accessConflict(
			"ACCESS_SYSTEM_ROLE_PROTECTED",
			"Administrator role is protected",
			"The framework-managed Administrator role cannot be deleted.",
		)
	}
	if role.UserCount > 0 {
		return RoleResponse{}, accessConflict(
			"ACCESS_ROLE_IN_USE",
			"Role is in use",
			"Remove the role from every user before deleting it.",
		)
	}
	registered := a.permissionScopeCatalog()
	for _, grant := range role.Grants {
		if _, current := registered[grant.Permission.Code]; !current {
			continue
		}
		if err := requireDelegableGrant(
			principal,
			grant.Permission.Code,
			frameworkauthz.GrantScope(grant.Scope),
		); err != nil {
			return RoleResponse{}, err
		}
	}
	if err := tx.Exec("DELETE FROM role_permissions WHERE role_id = ?", roleID).Error; err != nil {
		return RoleResponse{}, err
	}
	if err := tx.Delete(&domain.Role{}, "id = ?", roleID).Error; err != nil {
		return RoleResponse{}, err
	}
	return role, nil
}

func accessNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
