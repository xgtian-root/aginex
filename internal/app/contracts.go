package app

import (
	"time"

	"github.com/xgtian-root/aginex/framework/httpx"
)

type Problem = httpx.Problem

type LoginRequest struct {
	Email    string `json:"email" format:"email" maxLength:"320" binding:"required,email,max=320"`
	Password string `json:"password" writeOnly:"true" binding:"required"`
}

type CSRFTokenResponse struct {
	Token      string `json:"token" minLength:"43" maxLength:"43"`
	HeaderName string `json:"headerName"`
}

type HealthResponse struct {
	Status  string    `json:"status" enum:"ok,ready"`
	Time    time.Time `json:"time"`
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
}

type UserResponse struct {
	ID            string                `json:"id" format:"uuid"`
	Email         string                `json:"email" format:"email"`
	DisplayName   string                `json:"displayName"`
	Status        string                `json:"status" enum:"active,disabled"`
	Roles         []RoleSummaryResponse `json:"roles" nullable:"false"`
	Administrator bool                  `json:"administrator"`
	Permissions   []string              `json:"permissions" nullable:"false"`
	Grants        []UserGrantResponse   `json:"grants" nullable:"false"`
	CreatedAt     time.Time             `json:"createdAt"`
	UpdatedAt     time.Time             `json:"updatedAt"`
}

type UserListResponse struct {
	ID            string                `json:"id" format:"uuid"`
	Email         string                `json:"email" format:"email"`
	DisplayName   string                `json:"displayName"`
	Status        string                `json:"status" enum:"active,disabled"`
	Roles         []RoleSummaryResponse `json:"roles" nullable:"false"`
	Administrator bool                  `json:"administrator"`
	CreatedAt     time.Time             `json:"createdAt"`
	UpdatedAt     time.Time             `json:"updatedAt"`
}

type ProductRequest struct {
	Name       string `json:"name" minLength:"2" maxLength:"240" binding:"required,min=2,max=240"`
	SKU        string `json:"sku" minLength:"2" maxLength:"120" binding:"required,min=2,max=120"`
	PriceCents int64  `json:"priceCents" minimum:"0" binding:"min=0"`
	Status     string `json:"status" enum:"draft,active,archived" binding:"required,oneof=draft active archived"`
}

type ProductResponse struct {
	ID         string    `json:"id" format:"uuid"`
	Name       string    `json:"name"`
	SKU        string    `json:"sku"`
	PriceCents int64     `json:"priceCents"`
	Status     string    `json:"status" enum:"draft,active,archived"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type PermissionResponse struct {
	ID            string    `json:"id" format:"uuid"`
	Code          string    `json:"code" pattern:"^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$"`
	Description   string    `json:"description"`
	AllowedScopes []string  `json:"allowedScopes" nullable:"false" enum:"own,all"`
	CreatedAt     time.Time `json:"createdAt"`
}

type RoleResponse struct {
	ID            string              `json:"id" format:"uuid"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	SystemManaged bool                `json:"systemManaged"`
	UserCount     int64               `json:"userCount" minimum:"0"`
	Grants        []RoleGrantResponse `json:"grants" nullable:"false"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
}

type AuditLogResponse struct {
	ID         string         `json:"id" format:"uuid"`
	ActorID    *string        `json:"actorId,omitempty" maxLength:"160"`
	ActorKind  string         `json:"actorKind" enum:"user,system,service"`
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	ResourceID string         `json:"resourceId"`
	Result     string         `json:"result" enum:"success,failure"`
	RequestID  string         `json:"requestId"`
	Source     string         `json:"source"`
	Summary    string         `json:"summary"`
	IPAddress  string         `json:"ipAddress"`
	Before     map[string]any `json:"before,omitempty"`
	After      map[string]any `json:"after,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type DashboardSummaryResponse struct {
	Products          int64     `json:"products"`
	Users             int64     `json:"users"`
	EventsLast24Hours int64     `json:"eventsLast24Hours"`
	GeneratedAt       time.Time `json:"generatedAt"`
}

// JobResponse intentionally omits payloads, hashes, idempotency keys,
// traceparent values, and raw failure messages. Those fields can contain
// business data or credentials and are not part of the operator HTTP contract.
type JobResponse struct {
	ID            string     `json:"id" format:"uuid"`
	Type          string     `json:"type" maxLength:"120"`
	Version       uint       `json:"version" minimum:"1"`
	State         string     `json:"state" enum:"pending,running,succeeded,failed,dead"`
	ScheduledAt   time.Time  `json:"scheduledAt"`
	Attempts      int        `json:"attempts" minimum:"0"`
	MaxAttempts   int        `json:"maxAttempts" minimum:"1"`
	LockedBy      string     `json:"lockedBy,omitempty" maxLength:"128"`
	LockedAt      *time.Time `json:"lockedAt,omitempty"`
	HeartbeatAt   *time.Time `json:"heartbeatAt,omitempty"`
	HasError      bool       `json:"hasError"`
	CreatedByKind string     `json:"createdByKind" enum:"user,system"`
	CreatedByID   string     `json:"createdById" maxLength:"160"`
	RequestID     string     `json:"requestId,omitempty" maxLength:"64"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
}

type JobRetryResponse struct {
	ID    string `json:"id" format:"uuid"`
	State string `json:"state" enum:"pending"`
}

type Page[T any] struct {
	Items    []T   `json:"items" nullable:"false"`
	Page     int   `json:"page" minimum:"1"`
	PageSize int   `json:"pageSize" minimum:"1" maximum:"100"`
	Total    int64 `json:"total" minimum:"0"`
}
