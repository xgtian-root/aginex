package domain

import (
	"time"

	"github.com/xgtian-root/aginex/backend/framework/audit"
	"gorm.io/gorm"
)

type User struct {
	ID          string `gorm:"primaryKey;size:36"`
	Email       string `gorm:"uniqueIndex;size:320"`
	DisplayName string `gorm:"size:200"`
	Status      string `gorm:"size:32"`
	Roles       []Role `gorm:"many2many:user_roles"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

const (
	IdentityProviderPassword = "password"
	IdentityStatusActive     = "active"
)

type UserIdentity struct {
	ID             string `gorm:"primaryKey;size:36"`
	UserID         string `gorm:"index;size:36"`
	Provider       string `gorm:"size:64"`
	Subject        string `gorm:"size:320"`
	CredentialHash string `gorm:"type:text" json:"-"`
	Status         string `gorm:"size:32"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Role struct {
	ID          string       `gorm:"primaryKey;size:36" json:"id"`
	Name        string       `gorm:"uniqueIndex;size:100" json:"name"`
	Description string       `gorm:"type:text" json:"description"`
	Permissions []Permission `gorm:"many2many:role_permissions" json:"permissions"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

type Permission struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	Code        string    `gorm:"uniqueIndex;size:160" json:"code"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

// RolePermission is the explicit role_permissions join model. The database
// migration owns the table; declaring the join here keeps grant scope visible
// to access-management services instead of losing it through GORM's default
// many-to-many association model.
type RolePermission struct {
	RoleID       string `gorm:"primaryKey;size:36"`
	PermissionID string `gorm:"primaryKey;size:36"`
	Scope        string `gorm:"size:16"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}

type Session struct {
	ID         string    `gorm:"primaryKey;size:36"`
	UserID     string    `gorm:"index;size:36"`
	TokenHash  string    `gorm:"uniqueIndex;size:64"`
	ExpiresAt  time.Time `gorm:"index"`
	CreatedAt  time.Time
	LastSeenAt time.Time
	IPAddress  string `gorm:"size:64"`
	UserAgent  string `gorm:"type:text"`
}

type AuditLog struct {
	ID         string                `gorm:"primaryKey;size:36" json:"id"`
	ActorID    *string               `gorm:"size:160" json:"actorId"`
	ActorKind  string                `gorm:"size:32;not null;default:user" json:"actorKind"`
	Action     string                `gorm:"size:160" json:"action"`
	Resource   string                `gorm:"size:120" json:"resource"`
	ResourceID string                `gorm:"size:160" json:"resourceId"`
	Result     string                `gorm:"size:32;not null;default:success" json:"result"`
	Source     string                `gorm:"size:64;not null;default:http" json:"source"`
	Summary    string                `gorm:"type:text" json:"summary"`
	RequestID  string                `gorm:"size:64" json:"requestId"`
	IPAddress  string                `gorm:"size:64" json:"ipAddress"`
	Before     audit.SanitizedFields `gorm:"column:sanitized_before;not null" json:"before"`
	After      audit.SanitizedFields `gorm:"column:sanitized_after;not null" json:"after"`
	CreatedAt  time.Time             `gorm:"index" json:"createdAt"`
}

type Product struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	Name       string    `gorm:"size:240" json:"name"`
	SKU        string    `gorm:"uniqueIndex;size:120" json:"sku"`
	PriceCents int64     `json:"priceCents"`
	Status     string    `gorm:"size:32" json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type FileObject struct {
	ID               string     `gorm:"primaryKey;size:36" json:"id"`
	StorageProfileID *string    `gorm:"column:storage_profile_id;index;size:36" json:"storageProfileId,omitempty"`
	Provider         string     `gorm:"size:32" json:"provider"`
	Bucket           string     `gorm:"size:240" json:"bucket"`
	ObjectKey        string     `gorm:"uniqueIndex;size:700" json:"objectKey"`
	OriginalName     string     `gorm:"size:500" json:"originalName"`
	ContentType      string     `gorm:"size:160" json:"contentType"`
	Size             int64      `json:"size"`
	ETag             string     `gorm:"column:etag;size:240" json:"etag"`
	SHA256           string     `gorm:"column:sha256;size:64" json:"sha256"`
	Width            int        `json:"width"`
	Height           int        `json:"height"`
	OwnerID          string     `gorm:"index;size:36" json:"ownerId"`
	Visibility       string     `gorm:"size:32" json:"visibility"`
	Status           string     `gorm:"size:32" json:"status"`
	UploadExpiresAt  *time.Time `gorm:"column:upload_expires_at" json:"-"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DeletedAt        *time.Time `gorm:"index" json:"deletedAt,omitempty"`
}

type FileUploadSessionStatus string

const (
	FileUploadSessionStatusActive     FileUploadSessionStatus = "active"
	FileUploadSessionStatusCompleting FileUploadSessionStatus = "completing"
	FileUploadSessionStatusVerifying  FileUploadSessionStatus = "verifying"
	FileUploadSessionStatusCompleted  FileUploadSessionStatus = "completed"
	FileUploadSessionStatusCancelling FileUploadSessionStatus = "cancelling"
	FileUploadSessionStatusCancelled  FileUploadSessionStatus = "cancelled"
	FileUploadSessionStatusExpiring   FileUploadSessionStatus = "expiring"
	FileUploadSessionStatusExpired    FileUploadSessionStatus = "expired"
)

// FileUploadSession is durable transfer state for one FileObject. The
// provider upload identifier is an internal capability and must never be
// serialized into an API or audit payload.
type FileUploadSession struct {
	ID                string                  `gorm:"primaryKey;size:36" json:"id"`
	FileID            string                  `gorm:"uniqueIndex;size:36" json:"fileId"`
	ProviderUploadID  string                  `gorm:"size:1024" json:"-"`
	ResumeFingerprint string                  `gorm:"size:64" json:"-"`
	PartSize          int64                   `json:"partSize"`
	PartCount         int                     `json:"partCount"`
	Status            FileUploadSessionStatus `gorm:"size:32;index:idx_file_upload_sessions_status_expires,priority:1" json:"status"`
	ExpiresAt         time.Time               `gorm:"index:idx_file_upload_sessions_status_expires,priority:2" json:"expiresAt"`
	CreatedAt         time.Time               `json:"createdAt"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}

// FileUploadPart records only provider-confirmed parts. ETags are opaque
// provider completion capabilities and remain server-side.
type FileUploadPart struct {
	SessionID   string    `gorm:"primaryKey;size:36" json:"sessionId"`
	PartNumber  int       `gorm:"primaryKey" json:"partNumber"`
	Size        int64     `json:"size"`
	ETag        string    `gorm:"column:etag;size:240" json:"-"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}
