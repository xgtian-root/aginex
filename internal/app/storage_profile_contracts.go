package app

import "time"

type StorageProfileRequest struct {
	ID              string  `json:"id,omitempty" format:"uuid"`
	Name            string  `json:"name" minLength:"1" maxLength:"120"`
	Provider        string  `json:"provider" enum:"aliyun-oss,aws-s3,minio,cloudflare-r2"`
	Bucket          string  `json:"bucket,omitempty" maxLength:"240"`
	Region          string  `json:"region,omitempty" maxLength:"120"`
	Endpoint        string  `json:"endpoint,omitempty" maxLength:"2048"`
	AccountID       string  `json:"accountId,omitempty" maxLength:"128"`
	AccessKeyID     *string `json:"accessKeyId,omitempty" maxLength:"512" writeOnly:"true"`
	AccessKeySecret *string `json:"accessKeySecret,omitempty" maxLength:"2048" writeOnly:"true"`
}

type StorageProfileResponse struct {
	ID             string    `json:"id" format:"uuid"`
	Name           string    `json:"name"`
	Provider       string    `json:"provider" enum:"local,aliyun-oss,aws-s3,minio,cloudflare-r2"`
	Driver         string    `json:"driver" enum:"local,s3,oss"`
	Status         string    `json:"status" enum:"available,archived"`
	Bucket         string    `json:"bucket,omitempty"`
	Region         string    `json:"region,omitempty"`
	Endpoint       string    `json:"endpoint,omitempty"`
	AccountID      string    `json:"accountId,omitempty"`
	LocalRoot      string    `json:"localRoot,omitempty"`
	AuthConfigured bool      `json:"authConfigured"`
	Used           bool      `json:"used"`
	Active         bool      `json:"active"`
	PendingActive  bool      `json:"pendingActive"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type StorageSettingsResponse struct {
	RuntimeActiveProfileID  string                   `json:"runtimeActiveProfileId" format:"uuid"`
	PendingActiveProfileID  string                   `json:"pendingActiveProfileId" format:"uuid"`
	EnvironmentManaged      bool                     `json:"environmentManaged"`
	RestartRequired         bool                     `json:"restartRequired"`
	Revision                uint64                   `json:"revision" minimum:"1"`
	RuntimeRevision         uint64                   `json:"runtimeRevision" minimum:"1"`
	UnboundFileCount        int64                    `json:"unboundFileCount" minimum:"0"`
	RuntimeFileUploadPolicy FileUploadPolicyResponse `json:"runtimeFileUploadPolicy"`
	PendingFileUploadPolicy FileUploadPolicyResponse `json:"pendingFileUploadPolicy"`
}

type StorageProfileTestResponse struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider"`
}
