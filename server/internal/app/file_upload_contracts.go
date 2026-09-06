package app

import "time"

const (
	absoluteMaxUploadBytes  int64 = 1 << 30
	multipartThresholdBytes int64 = 32 << 20
	multipartPartSizeBytes  int64 = 32 << 20
	multipartSessionSeconds int64 = 24 * 60 * 60
	maxUploadBatchFiles           = 20
	maxPartOperationBatch         = 8
)

type UploadIntentRequest struct {
	Filename          string `json:"filename" minLength:"1" maxLength:"500" binding:"required,min=1,max=500"`
	ContentType       string `json:"contentType" minLength:"1" maxLength:"160" binding:"required,min=1,max=160"`
	Size              int64  `json:"size" minimum:"1" maximum:"1073741824" binding:"required,min=1,max=1073741824"`
	Visibility        string `json:"visibility" enum:"private,public" binding:"required,oneof=private public"`
	Strategy          string `json:"strategy,omitempty" enum:"single,resumable" binding:"omitempty,oneof=single resumable"`
	ResumeFingerprint string `json:"resumeFingerprint,omitempty" pattern:"^[a-f0-9]{64}$" binding:"omitempty,len=64,hexadecimal"`
}

type FileResponse struct {
	ID                 string    `json:"id" format:"uuid"`
	Provider           string    `json:"provider" enum:"local,s3,oss"`
	StorageProfileID   string    `json:"storageProfileId,omitempty" format:"uuid"`
	StorageProfileName string    `json:"storageProfileName,omitempty"`
	StorageProvider    string    `json:"storageProvider,omitempty" enum:"local,aliyun-oss,aws-s3,minio,cloudflare-r2"`
	OriginalName       string    `json:"originalName"`
	ContentType        string    `json:"contentType"`
	PreviewKind        string    `json:"previewKind" enum:"image,pdf,none"`
	Size               int64     `json:"size"`
	SHA256             string    `json:"sha256" pattern:"^(|[a-f0-9]{64})$"`
	Width              int       `json:"width" minimum:"0"`
	Height             int       `json:"height" minimum:"0"`
	Visibility         string    `json:"visibility" enum:"private,public"`
	Status             string    `json:"status" enum:"pending,ready,invalid,deleting,delete_failed,deleted"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type SignedRequestResponse struct {
	URL       string            `json:"url" format:"uri"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type UploadIntentResponse struct {
	Strategy string                 `json:"strategy" enum:"single,resumable"`
	File     FileResponse           `json:"file"`
	Upload   *SignedRequestResponse `json:"upload,omitempty"`
	Session  *UploadSessionResponse `json:"session,omitempty"`
}

type FileUploadPolicyResponse struct {
	MaxUploadBytes          int64 `json:"maxUploadBytes" minimum:"1048576" maximum:"1073741824"`
	ResumableUploadsEnabled bool  `json:"resumableUploadsEnabled"`
}

type FileUploadPolicyUpdateRequest struct {
	MaxUploadBytes          int64 `json:"maxUploadBytes" minimum:"1048576" maximum:"1073741824" multipleOf:"1048576" binding:"required,min=1048576,max=1073741824"`
	ResumableUploadsEnabled bool  `json:"resumableUploadsEnabled"`
}

type UploadPolicyResponse struct {
	MaxUploadBytes          int64 `json:"maxUploadBytes" minimum:"1048576" maximum:"1073741824"`
	ResumableUploadsEnabled bool  `json:"resumableUploadsEnabled"`
	ResumableAvailable      bool  `json:"resumableAvailable"`
	MultipartThresholdBytes int64 `json:"multipartThresholdBytes" minimum:"33554432" maximum:"33554432"`
	MultipartPartSizeBytes  int64 `json:"multipartPartSizeBytes" minimum:"33554432" maximum:"33554432"`
	SessionTTLSeconds       int64 `json:"sessionTtlSeconds" minimum:"86400" maximum:"86400"`
	MaxBatchFiles           int   `json:"maxBatchFiles" minimum:"20" maximum:"20"`
}

type UploadPartResponse struct {
	PartNumber  int       `json:"partNumber" minimum:"1" maximum:"32"`
	Size        int64     `json:"size" minimum:"1" maximum:"33554432"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

type UploadSessionResponse struct {
	ID             string               `json:"id" format:"uuid"`
	File           FileResponse         `json:"file"`
	Status         string               `json:"status" enum:"active,completing,verifying,completed,cancelling,cancelled,expiring,expired"`
	PartSize       int64                `json:"partSize" minimum:"33554432" maximum:"33554432"`
	PartCount      int                  `json:"partCount" minimum:"2" maximum:"32"`
	CompletedParts []UploadPartResponse `json:"completedParts" nullable:"false"`
	ExpiresAt      time.Time            `json:"expiresAt"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

type ResumeUploadSessionRequest struct {
	Fingerprint string `json:"fingerprint" pattern:"^[a-f0-9]{64}$" binding:"required,len=64,hexadecimal"`
}

type SignUploadPartsRequest struct {
	PartNumbers []int `json:"partNumbers" minItems:"1" maxItems:"8" nullable:"false" binding:"required,min=1,max=8,dive,min=1,max=32"`
}

type SignedUploadPartResponse struct {
	PartNumber int                   `json:"partNumber" minimum:"1" maximum:"32"`
	Size       int64                 `json:"size" minimum:"1" maximum:"33554432"`
	Upload     SignedRequestResponse `json:"upload"`
}

type SignUploadPartsResponse struct {
	Items []SignedUploadPartResponse `json:"items" nullable:"false"`
}

type AckUploadPartRequest struct {
	PartNumber int    `json:"partNumber" minimum:"1" maximum:"32" binding:"required,min=1,max=32"`
	ETag       string `json:"etag" minLength:"1" maxLength:"240" writeOnly:"true" binding:"required,min=1,max=240"`
}

type AckUploadPartsRequest struct {
	Parts []AckUploadPartRequest `json:"parts" minItems:"1" maxItems:"8" nullable:"false" binding:"required,min=1,max=8,dive"`
}
