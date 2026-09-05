package app

import (
	"testing"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/internal/config"
)

func TestStorageProfileProviderPresets(t *testing.T) {
	app := &App{cfg: config.Config{Environment: "development"}}
	accessKeyID := "key-id"
	accessKeySecret := "key-secret"
	tests := []struct {
		name      string
		request   StorageProfileRequest
		driver    string
		region    string
		endpoint  string
		pathStyle bool
	}{
		{
			name: "Alibaba OSS", driver: "oss", region: "cn-hangzhou",
			endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
			request:  StorageProfileRequest{Name: "OSS", Provider: "aliyun-oss", Bucket: "images", Region: "cn-hangzhou", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", AccessKeyID: &accessKeyID, AccessKeySecret: &accessKeySecret},
		},
		{
			name: "AWS S3", driver: "s3", region: "us-west-2",
			request: StorageProfileRequest{Name: "S3", Provider: "aws-s3", Bucket: "images", Region: "us-west-2", AccessKeyID: &accessKeyID, AccessKeySecret: &accessKeySecret},
		},
		{
			name: "MinIO", driver: "s3", region: "us-east-1", endpoint: "http://127.0.0.1:9000", pathStyle: true,
			request: StorageProfileRequest{Name: "MinIO", Provider: "minio", Bucket: "images", Endpoint: "http://127.0.0.1:9000", AccessKeyID: &accessKeyID, AccessKeySecret: &accessKeySecret},
		},
		{
			name: "Cloudflare R2", driver: "s3", region: "auto", endpoint: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com", pathStyle: true,
			request: StorageProfileRequest{Name: "R2", Provider: "cloudflare-r2", Bucket: "images", AccountID: "0123456789abcdef0123456789abcdef", AccessKeyID: &accessKeyID, AccessKeySecret: &accessKeySecret},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, err := app.profileFromRequest(test.request, nil)
			if err != nil {
				t.Fatal(err)
			}
			storage := profile.StorageConfig()
			if storage.Driver != test.driver || storage.Region != test.region ||
				storage.Endpoint != test.endpoint || storage.ForcePathStyle != test.pathStyle {
				t.Fatalf("storage preset = %#v", storage)
			}
		})
	}
}

func TestStorageProfileUpdateRetainsOrRotatesCredentialPair(t *testing.T) {
	app := &App{cfg: config.Config{Environment: "development"}}
	current := config.StorageProfile{
		ID: uuid.NewString(), Name: "MinIO", Provider: config.StorageProviderMinIO,
		Status: config.StorageProfileAvailable, Bucket: "images", Region: "us-east-1",
		Endpoint: "http://127.0.0.1:9000", AccessKeyID: "old-id", AccessKeySecret: "old-secret",
		CredentialSource: config.StorageCredentialsManaged,
	}
	request := StorageProfileRequest{Name: current.Name, Provider: string(current.Provider), Bucket: current.Bucket, Region: current.Region, Endpoint: current.Endpoint}
	retained, err := app.profileFromRequest(request, &current)
	if err != nil {
		t.Fatal(err)
	}
	if retained.AccessKeyID != "old-id" || retained.AccessKeySecret != "old-secret" {
		t.Fatalf("retained credentials = %#v", retained)
	}
	newID, newSecret := "new-id", "new-secret"
	request.AccessKeyID, request.AccessKeySecret = &newID, &newSecret
	rotated, err := app.profileFromRequest(request, &current)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AccessKeyID != newID || rotated.AccessKeySecret != newSecret ||
		rotated.CredentialSource != config.StorageCredentialsManaged {
		t.Fatalf("rotated credentials = %#v", rotated)
	}
}
