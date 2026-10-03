package files

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
)

// These tests exercise real provider adapters and SDK requests against a local
// HTTP fake. They do not assert access to a real S3 or OSS vendor environment.
func TestOpenCloudAdaptersUseOriginalProfile(t *testing.T) {
	for _, provider := range []config.StorageProvider{config.StorageProviderMinIO, config.StorageProviderAlibabaOSS} {
		t.Run(string(provider), func(t *testing.T) {
			db, err := database.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "read.db")})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			// Read-only adapter fixture. The application migration tests own the
			// complete schema; this fixture needs only metadata read by Open.
			if err := db.Exec(`CREATE TABLE file_objects (
				id TEXT PRIMARY KEY, storage_profile_id TEXT, provider TEXT,
				bucket TEXT, object_key TEXT, status TEXT, deleted_at DATETIME
			)`).Error; err != nil {
				t.Fatal(err)
			}
			profileA := config.StorageProfile{ID: uuid.NewString(), Provider: provider, Bucket: "original-bucket"}
			profileB := config.StorageProfile{ID: uuid.NewString(), Provider: provider, Bucket: "new-bucket"}
			key, id := "files/verified.png", uuid.NewString()
			if err := db.Exec("INSERT INTO file_objects (id, storage_profile_id, provider, bucket, object_key, status) VALUES (?, ?, ?, ?, ?, ?)", id, profileA.ID, profileA.StorageConfig().Driver, profileA.Bucket, key, "ready").Error; err != nil {
				t.Fatal(err)
			}
			requestsA, requestsB := 0, 0
			clientA := fakeHTTPClient(func(request *http.Request) (*http.Response, error) {
				requestsA++
				if request.Method != http.MethodGet || request.URL.Path != "/original-bucket/"+key || request.Header.Get("Authorization") == "" {
					t.Errorf("provider request omitted original target or authentication: method=%s path=%s signed=%t", request.Method, request.URL.Path, request.Header.Get("Authorization") != "")
				}
				return providerResponse(request, "from original provider"), nil
			})
			clientB := fakeHTTPClient(func(request *http.Request) (*http.Response, error) {
				requestsB++
				return providerResponse(request, "WRONG active provider"), nil
			})
			resolver := &fakeStorageResolver{
				profiles: map[string]config.StorageProfile{profileA.ID: profileA, profileB.ID: profileB},
				stores: map[string]storage.Storage{
					profileA.ID: cloudAdapter(provider, profileA.Bucket, clientA),
					profileB.ID: cloudAdapter(provider, profileB.Bucket, clientB),
				},
			}
			service := &service{db: db, registry: resolver}
			metadata, reader, err := service.Open(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || metadata.ID != id || string(body) != "from original provider" || requestsA != 1 || requestsB != 0 {
				t.Fatalf("provider read body=%q A=%d B=%d errors=%v/%v", body, requestsA, requestsB, readErr, closeErr)
			}
			delete(resolver.stores, profileA.ID)
			if _, _, err := service.Open(t.Context(), id); !errors.Is(err, frameworkfiles.ErrStorageUnavailable) {
				t.Fatalf("unavailable original profile error=%v", err)
			}
			delete(resolver.profiles, profileA.ID)
			if _, _, err := service.Open(t.Context(), id); !errors.Is(err, frameworkfiles.ErrStorageUnavailable) {
				t.Fatalf("missing original profile error=%v", err)
			}
			if requestsA != 1 || requestsB != 0 {
				t.Fatalf("unavailable original profile caused fallback: A=%d B=%d", requestsA, requestsB)
			}
		})
	}
}

type fakeHTTPClient func(*http.Request) (*http.Response, error)

func (client fakeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client(request)
}

func (client fakeHTTPClient) RoundTrip(request *http.Request) (*http.Response, error) {
	return client(request)
}

func providerResponse(request *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK, Request: request,
		Header: http.Header{"Content-Type": {"image/png"}, "ETag": {`"test-etag"`}},
		Body:   io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)),
	}
}

func cloudAdapter(provider config.StorageProvider, bucket string, client fakeHTTPClient) storage.Storage {
	if provider == config.StorageProviderAlibabaOSS {
		cfg := oss.LoadDefaultConfig().WithRegion("cn-hangzhou").
			WithEndpoint("https://oss-cn-hangzhou.aliyuncs.com").
			WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider("test-key", "test-secret")).
			WithHttpClient(&http.Client{Transport: client}).WithUsePathStyle(true)
		return storage.NewOSS(cfg, bucket, storage.DefaultFilePolicy())
	}
	awsConfig := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""), HTTPClient: client}
	s3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String("https://objects.example.test")
		options.UsePathStyle = true
	})
	return storage.NewS3(s3Client, bucket, storage.DefaultFilePolicy())
}

type fakeStorageResolver struct {
	profiles map[string]config.StorageProfile
	stores   map[string]storage.Storage
}

func (resolver *fakeStorageResolver) Profile(id string) (config.StorageProfile, bool) {
	profile, ok := resolver.profiles[id]
	return profile, ok
}

func (resolver *fakeStorageResolver) Resolve(id string) (storage.Storage, error) {
	store, ok := resolver.stores[id]
	if !ok {
		return nil, storage.ErrStorageProfileDegraded
	}
	return store, nil
}
