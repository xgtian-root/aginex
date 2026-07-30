package storage

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3 struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
	policy    Policy
}

func NewS3(client *s3.Client, bucket string, policy Policy) *S3 {
	return &S3{client: client, presigner: s3.NewPresignClient(client), bucket: bucket, policy: policy}
}

func (s *S3) CreateUpload(ctx context.Context, request UploadRequest) (SignedRequest, error) {
	if err := s.policy.Validate(request); err != nil {
		return SignedRequest{}, err
	}
	expires := request.Expires
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	result, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(request.Key), ContentType: aws.String(request.ContentType),
	}, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method, Headers: flattenHeaders(result.SignedHeader), ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (s *S3) SignRead(ctx context.Context, key string, expires time.Duration) (SignedRequest, error) {
	if err := ValidateKey(key); err != nil {
		return SignedRequest{}, err
	}
	result, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	}, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method, Headers: flattenHeaders(result.SignedHeader), ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key: key, Size: aws.ToInt64(result.ContentLength), ContentType: aws.ToString(result.ContentType),
		ETag: strings.Trim(aws.ToString(result.ETag), `"`), ModifiedAt: aws.ToTime(result.LastModified),
	}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	return err
}

func flattenHeaders(input map[string][]string) map[string]string {
	headers := make(map[string]string, len(input))
	for key, values := range input {
		headers[key] = strings.Join(values, ",")
	}
	return headers
}
