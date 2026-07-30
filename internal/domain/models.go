package domain

import "time"

type User struct {
	ID           string `gorm:"primaryKey;size:36"`
	Email        string `gorm:"uniqueIndex;size:320"`
	DisplayName  string `gorm:"size:200"`
	PasswordHash string `json:"-"`
	Status       string `gorm:"size:32"`
	Roles        []Role `gorm:"many2many:user_roles"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	ActorID    *string   `gorm:"size:36" json:"actorId"`
	Action     string    `gorm:"size:160" json:"action"`
	Resource   string    `gorm:"size:120" json:"resource"`
	ResourceID string    `gorm:"size:160" json:"resourceId"`
	Summary    string    `gorm:"type:text" json:"summary"`
	RequestID  string    `gorm:"size:64" json:"requestId"`
	IPAddress  string    `gorm:"size:64" json:"ipAddress"`
	CreatedAt  time.Time `gorm:"index" json:"createdAt"`
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
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	Provider     string    `gorm:"size:32" json:"provider"`
	Bucket       string    `gorm:"size:240" json:"bucket"`
	ObjectKey    string    `gorm:"uniqueIndex;size:700" json:"objectKey"`
	OriginalName string    `gorm:"size:500" json:"originalName"`
	ContentType  string    `gorm:"size:160" json:"contentType"`
	Size         int64     `json:"size"`
	ETag         string    `gorm:"column:etag;size:240" json:"etag"`
	OwnerID      string    `gorm:"index;size:36" json:"ownerId"`
	Visibility   string    `gorm:"size:32" json:"visibility"`
	Status       string    `gorm:"size:32" json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
