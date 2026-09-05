package app

type CreateUserRequest struct {
	Email       string   `json:"email" format:"email" maxLength:"320" binding:"required,email,max=320"`
	DisplayName string   `json:"displayName" minLength:"1" maxLength:"200" binding:"required,min=1,max=200"`
	Password    string   `json:"password" writeOnly:"true" binding:"required"`
	RoleIDs     []string `json:"roleIds" nullable:"false" binding:"required,dive,uuid"`
}

type UpdateUserRequest struct {
	DisplayName string `json:"displayName" minLength:"1" maxLength:"200" binding:"required,min=1,max=200"`
}

type ReplaceUserRolesRequest struct {
	RoleIDs []string `json:"roleIds" nullable:"false" binding:"required,dive,uuid"`
}

type ResetUserPasswordRequest struct {
	Password string `json:"password" writeOnly:"true" binding:"required"`
}

type RoleSummaryResponse struct {
	ID            string `json:"id" format:"uuid"`
	Name          string `json:"name"`
	SystemManaged bool   `json:"systemManaged"`
}

type UserGrantResponse struct {
	Permission string `json:"permission" pattern:"^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$"`
	Scope      string `json:"scope" enum:"own,all"`
}

type RoleGrantInput struct {
	PermissionCode string `json:"permissionCode" pattern:"^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$" binding:"required"`
	Scope          string `json:"scope" enum:"own,all" binding:"required,oneof=own all"`
}

type RoleGrantResponse struct {
	Permission PermissionResponse `json:"permission"`
	Scope      string             `json:"scope" enum:"own,all"`
}

type CreateRoleRequest struct {
	Name        string           `json:"name" minLength:"1" maxLength:"100" binding:"required,min=1,max=100"`
	Description string           `json:"description" maxLength:"2000" binding:"max=2000"`
	Grants      []RoleGrantInput `json:"grants" nullable:"false" binding:"required,dive"`
}

type UpdateRoleRequest struct {
	Name        string `json:"name" minLength:"1" maxLength:"100" binding:"required,min=1,max=100"`
	Description string `json:"description" maxLength:"2000" binding:"max=2000"`
}

type ReplaceRoleGrantsRequest struct {
	Grants []RoleGrantInput `json:"grants" nullable:"false" binding:"required,dive"`
}
