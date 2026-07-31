package storage

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

type OSS struct {
	client *oss.Client
	bucket string
	policy Policy
}

func NewOSS(cfg *oss.Config, bucket string, policy Policy) *OSS {
	configured := cfg.Copy()
	additionalHeaders := append([]string(nil), configured.AdditionalHeaders...)
	if !containsHeader(additionalHeaders, "content-length") {
		additionalHeaders = append(additionalHeaders, "content-length")
	}
	configured.
		WithSignatureVersion(oss.SignatureVersionV4).
		WithAdditionalHeaders(additionalHeaders)
	return &OSS{client: oss.NewClient(&configured), bucket: bucket, policy: policy}
}

func (o *OSS) CreateUpload(ctx context.Context, request UploadRequest) (SignedRequest, error) {
	if err := o.policy.Validate(request); err != nil {
		return SignedRequest{}, err
	}
	expires := request.Expires
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	result, err := o.client.Presign(ctx, &oss.PutObjectRequest{
		Bucket:      oss.Ptr(o.bucket),
		Key:         oss.Ptr(request.Key),
		ContentType: oss.Ptr(request.ContentType),
		RequestCommon: oss.RequestCommon{Headers: map[string]string{
			"Content-Length": strconv.FormatInt(request.Size, 10),
		}},
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method, Headers: result.SignedHeaders, ExpiresAt: result.Expiration,
	}, nil
}

func (o *OSS) CheckReadiness(ctx context.Context) error {
	_, err := o.client.GetBucketInfo(
		ctx,
		&oss.GetBucketInfoRequest{Bucket: oss.Ptr(o.bucket)},
	)
	return err
}

func (o *OSS) SignRead(ctx context.Context, key string, expires time.Duration) (SignedRequest, error) {
	if err := ValidateKey(key); err != nil {
		return SignedRequest{}, err
	}
	result, err := o.client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method, Headers: result.SignedHeaders, ExpiresAt: result.Expiration,
	}, nil
}

func (o *OSS) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	result, err := o.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
	})
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key: key, Size: result.ContentLength, ContentType: oss.ToString(result.ContentType),
		ETag: strings.Trim(oss.ToString(result.ETag), `"`), ModifiedAt: oss.ToTime(result.LastModified),
	}, nil
}

func (o *OSS) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	result, err := o.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
	})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (o *OSS) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	_, err := o.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
	})
	return err
}

func containsHeader(headers []string, expected string) bool {
	for _, header := range headers {
		if strings.EqualFold(header, expected) {
			return true
		}
	}
	return false
}
