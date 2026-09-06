package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3 struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
	policy    FilePolicy
}

func NewS3(client *s3.Client, bucket string, policy FilePolicy) *S3 {
	return &S3{client: client, presigner: s3.NewPresignClient(client), bucket: bucket, policy: policy}
}

func (s *S3) CreateUpload(ctx context.Context, request UploadRequest) (SignedRequest, error) {
	if err := validateUpload(s.policy, request); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	result, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(request.Key),
		ContentLength:      aws.Int64(request.Size),
		ContentType:        aws.String(StoredContentType),
		ContentDisposition: aws.String("attachment"),
		// Prevent a signed single-upload request from replacing the object
		// after its bytes have been verified and the database row is ready.
		IfNoneMatch: aws.String("*"),
	}, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers:   flattenHeaders(result.SignedHeader),
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (s *S3) CheckReadiness(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	return err
}

func (s *S3) SignRead(ctx context.Context, key string, expires time.Duration) (SignedRequest, error) {
	return s.SignControlledRead(ctx, ControlledReadRequest{
		Key: key, Expires: expires, ContentType: StoredContentType,
		Disposition: ReadDispositionAttachment, Filename: "download",
	})
}

func (s *S3) SignControlledRead(
	ctx context.Context,
	request ControlledReadRequest,
) (SignedRequest, error) {
	contentType, disposition, expires, err := controlledReadValues(request)
	if err != nil {
		return SignedRequest{}, err
	}
	result, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(request.Key),
		ResponseContentType:        aws.String(contentType),
		ResponseContentDisposition: aws.String(disposition),
	}, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers:   flattenHeaders(result.SignedHeader),
		ExpiresAt: time.Now().UTC().Add(expires),
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
		ContentDisposition: aws.ToString(result.ContentDisposition),
		ETag:               strings.Trim(aws.ToString(result.ETag), `"`), ModifiedAt: aws.ToTime(result.LastModified),
	}, nil
}

func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
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

func (s *S3) InitiateMultipart(
	ctx context.Context,
	request UploadRequest,
) (MultipartUpload, error) {
	if err := validateUpload(s.policy, request); err != nil {
		return MultipartUpload{}, err
	}
	result, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(request.Key),
		ContentType:        aws.String(StoredContentType),
		ContentDisposition: aws.String("attachment"),
	})
	if err != nil {
		return MultipartUpload{}, err
	}
	if strings.TrimSpace(aws.ToString(result.UploadId)) == "" {
		return MultipartUpload{}, ErrInvalidMultipart
	}
	return MultipartUpload{Key: request.Key, ProviderUploadID: aws.ToString(result.UploadId)}, nil
}

func (s *S3) SignUploadPart(
	ctx context.Context,
	request MultipartPartRequest,
) (SignedRequest, error) {
	if err := validateMultipartPart(s.policy, request); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	result, err := s.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(request.Upload.Key),
		UploadId:      aws.String(request.Upload.ProviderUploadID),
		PartNumber:    aws.Int32(request.PartNumber),
		ContentLength: aws.Int64(request.Size),
	}, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers:   signedContentLength(flattenHeaders(result.SignedHeader), request.Size),
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (s *S3) ListUploadedParts(
	ctx context.Context,
	upload MultipartUpload,
) ([]UploadedPart, error) {
	if err := validateMultipartUpload(upload); err != nil {
		return nil, err
	}
	paginator := s3.NewListPartsPaginator(s.client, &s3.ListPartsInput{
		Bucket: aws.String(s.bucket), Key: aws.String(upload.Key), UploadId: aws.String(upload.ProviderUploadID),
	})
	parts := make([]UploadedPart, 0, 32)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if hasStorageErrorCode(err, "NoSuchUpload") {
				return nil, ErrMultipartNotFound
			}
			return nil, err
		}
		for _, part := range page.Parts {
			parts = append(parts, UploadedPart{
				PartNumber: aws.ToInt32(part.PartNumber),
				Size:       aws.ToInt64(part.Size),
				ETag:       aws.ToString(part.ETag),
			})
		}
	}
	return parts, nil
}

func (s *S3) CompleteMultipart(
	ctx context.Context,
	request MultipartCompleteRequest,
) (ObjectInfo, error) {
	if err := validateCompletion(request); err != nil {
		return ObjectInfo{}, err
	}
	if err := validateCompletionSize(s.policy, request.Parts); err != nil {
		return ObjectInfo{}, err
	}
	providerParts, err := s.ListUploadedParts(ctx, request.Upload)
	if err != nil {
		if errors.Is(err, ErrMultipartNotFound) {
			if info, statErr := s.Stat(ctx, request.Upload.Key); statErr == nil {
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}
	if !uploadedPartsMatch(request.Parts, providerParts) {
		return ObjectInfo{}, ErrInvalidMultipart
	}
	completed := make([]types.CompletedPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		completed = append(completed, types.CompletedPart{
			PartNumber: aws.Int32(part.PartNumber), ETag: aws.String(part.ETag),
		})
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(s.bucket), Key: aws.String(request.Upload.Key),
		UploadId:        aws.String(request.Upload.ProviderUploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		if hasStorageErrorCode(err, "NoSuchUpload") {
			if info, statErr := s.Stat(ctx, request.Upload.Key); statErr == nil {
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}
	return s.Stat(ctx, request.Upload.Key)
}

func (s *S3) AbortMultipart(ctx context.Context, upload MultipartUpload) error {
	if err := validateMultipartUpload(upload); err != nil {
		return err
	}
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: aws.String(s.bucket), Key: aws.String(upload.Key), UploadId: aws.String(upload.ProviderUploadID),
	})
	if hasStorageErrorCode(err, "NoSuchUpload") {
		return nil
	}
	return err
}

func flattenHeaders(input map[string][]string) map[string]string {
	headers := make(map[string]string, len(input))
	for key, values := range input {
		headers[key] = strings.Join(values, ",")
	}
	return headers
}

func hasStorageErrorCode(err error, expected string) bool {
	if err == nil {
		return false
	}
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && strings.EqualFold(coded.ErrorCode(), expected)
}
