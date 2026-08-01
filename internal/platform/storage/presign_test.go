package storage

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3CreateUploadBindsDeclaredSizeToSignature(t *testing.T) {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.com"),
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		),
		Region:       "us-east-1",
		UsePathStyle: true,
	})
	store := NewS3(client, "private-bucket", DefaultImagePolicy())

	assertCreateUploadBindsDeclaredSize(t, store, "X-Amz-SignedHeaders")
}

func TestOSSCreateUploadBindsDeclaredSizeToSignature(t *testing.T) {
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(
			osscredentials.NewStaticCredentialsProvider("access-key", "secret-key"),
		).
		WithRegion("cn-hangzhou").
		WithEndpoint("https://objects.example.com")
	store := NewOSS(cfg, "private-bucket", DefaultImagePolicy())

	assertCreateUploadBindsDeclaredSize(t, store, "x-oss-additional-headers")
}

func assertCreateUploadBindsDeclaredSize(
	t *testing.T,
	store Storage,
	signedHeadersParameter string,
) {
	t.Helper()
	const declaredSize int64 = 4097

	signed, err := store.CreateUpload(context.Background(), UploadRequest{
		Key:         "uploads/random-object.png",
		ContentType: "image/png",
		Size:        declaredSize,
		Expires:     time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(signed.Headers, "Content-Length"); got != strconv.FormatInt(declaredSize, 10) {
		t.Fatalf("signed Content-Length = %q, want %d", got, declaredSize)
	}
	if signedHeadersParameter != "" {
		parsed, err := url.Parse(signed.URL)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.ToLower(parsed.Query().Get(signedHeadersParameter)); !strings.Contains(got, "content-length") {
			t.Fatalf("%s = %q, want content-length bound to signature", signedHeadersParameter, got)
		}
	}
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
