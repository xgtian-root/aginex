package storage

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

type OSS struct {
	client *oss.Client
	bucket string
	policy FilePolicy
}

func NewOSS(cfg *oss.Config, bucket string, policy FilePolicy) *OSS {
	configured := cfg.Copy()
	additionalHeaders := append([]string(nil), configured.AdditionalHeaders...)
	for _, header := range []string{
		"content-length", "content-type", "content-disposition", "x-oss-forbid-overwrite",
	} {
		if !containsHeader(additionalHeaders, header) {
			additionalHeaders = append(additionalHeaders, header)
		}
	}
	configured.
		WithSignatureVersion(oss.SignatureVersionV4).
		WithAdditionalHeaders(additionalHeaders)
	return &OSS{client: oss.NewClient(&configured), bucket: bucket, policy: policy}
}

func (o *OSS) CreateUpload(ctx context.Context, request UploadRequest) (SignedRequest, error) {
	if err := validateUpload(o.policy, request); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	result, err := o.client.Presign(ctx, &oss.PutObjectRequest{
		Bucket:             oss.Ptr(o.bucket),
		Key:                oss.Ptr(request.Key),
		ContentType:        oss.Ptr(StoredContentType),
		ContentDisposition: oss.Ptr("attachment"),
		ContentLength:      oss.Ptr(request.Size),
		ForbidOverwrite:    oss.Ptr("true"),
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers: result.SignedHeaders, ExpiresAt: result.Expiration,
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
	return o.SignControlledRead(ctx, ControlledReadRequest{
		Key: key, Expires: expires, ContentType: StoredContentType,
		Disposition: ReadDispositionAttachment, Filename: "download",
	})
}

func (o *OSS) SignControlledRead(
	ctx context.Context,
	request ControlledReadRequest,
) (SignedRequest, error) {
	contentType, disposition, expires, err := controlledReadValues(request)
	if err != nil {
		return SignedRequest{}, err
	}
	result, err := o.client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Key),
		ResponseContentType: oss.Ptr(contentType), ResponseContentDisposition: oss.Ptr(disposition),
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers: result.SignedHeaders, ExpiresAt: result.Expiration,
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

func (o *OSS) InitiateMultipart(
	ctx context.Context,
	request UploadRequest,
) (MultipartUpload, error) {
	if err := validateUpload(o.policy, request); err != nil {
		return MultipartUpload{}, err
	}
	result, err := o.client.InitiateMultipartUpload(ctx, &oss.InitiateMultipartUploadRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Key),
		ContentType: oss.Ptr(StoredContentType), ContentDisposition: oss.Ptr("attachment"),
		ForbidOverwrite: oss.Ptr("true"), DisableAutoDetectMimeType: true,
	})
	if err != nil {
		return MultipartUpload{}, err
	}
	if strings.TrimSpace(oss.ToString(result.UploadId)) == "" {
		return MultipartUpload{}, ErrInvalidMultipart
	}
	return MultipartUpload{Key: request.Key, ProviderUploadID: oss.ToString(result.UploadId)}, nil
}

func (o *OSS) SignUploadPart(
	ctx context.Context,
	request MultipartPartRequest,
) (SignedRequest, error) {
	if err := validateMultipartPart(o.policy, request); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	result, err := o.client.Presign(ctx, &oss.UploadPartRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Upload.Key),
		UploadId: oss.Ptr(request.Upload.ProviderUploadID), PartNumber: request.PartNumber,
		ContentLength: oss.Ptr(request.Size),
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers: signedContentLength(result.SignedHeaders, request.Size), ExpiresAt: result.Expiration,
	}, nil
}

func (o *OSS) ListUploadedParts(
	ctx context.Context,
	upload MultipartUpload,
) ([]UploadedPart, error) {
	if err := validateMultipartUpload(upload); err != nil {
		return nil, err
	}
	parts := make([]UploadedPart, 0, 32)
	marker := int32(0)
	for {
		result, err := o.client.ListParts(ctx, &oss.ListPartsRequest{
			Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(upload.Key), UploadId: oss.Ptr(upload.ProviderUploadID),
			PartNumberMarker: marker, MaxParts: 1000,
		})
		if err != nil {
			if hasStorageErrorCode(err, "NoSuchUpload") {
				return nil, ErrMultipartNotFound
			}
			return nil, err
		}
		for _, part := range result.Parts {
			parts = append(parts, UploadedPart{
				PartNumber: part.PartNumber,
				Size:       part.Size,
				ETag:       oss.ToString(part.ETag),
			})
		}
		if !result.IsTruncated {
			break
		}
		if result.NextPartNumberMarker <= marker {
			return nil, ErrInvalidMultipart
		}
		marker = result.NextPartNumberMarker
	}
	return parts, nil
}

func (o *OSS) CompleteMultipart(
	ctx context.Context,
	request MultipartCompleteRequest,
) (ObjectInfo, error) {
	if err := validateCompletion(request); err != nil {
		return ObjectInfo{}, err
	}
	if err := validateCompletionSize(o.policy, request.Parts); err != nil {
		return ObjectInfo{}, err
	}
	providerParts, err := o.ListUploadedParts(ctx, request.Upload)
	if err != nil {
		if errors.Is(err, ErrMultipartNotFound) {
			if info, statErr := o.Stat(ctx, request.Upload.Key); statErr == nil {
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}
	if !uploadedPartsMatch(request.Parts, providerParts) {
		return ObjectInfo{}, ErrInvalidMultipart
	}
	parts := make([]oss.UploadPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		parts = append(parts, oss.UploadPart{PartNumber: part.PartNumber, ETag: oss.Ptr(part.ETag)})
	}
	_, err = o.client.CompleteMultipartUpload(ctx, &oss.CompleteMultipartUploadRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Upload.Key), UploadId: oss.Ptr(request.Upload.ProviderUploadID),
		ForbidOverwrite:         oss.Ptr("true"),
		CompleteMultipartUpload: &oss.CompleteMultipartUpload{Parts: parts},
	})
	if err != nil {
		if hasStorageErrorCode(err, "NoSuchUpload") {
			if info, statErr := o.Stat(ctx, request.Upload.Key); statErr == nil {
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}
	return o.Stat(ctx, request.Upload.Key)
}

func (o *OSS) AbortMultipart(ctx context.Context, upload MultipartUpload) error {
	if err := validateMultipartUpload(upload); err != nil {
		return err
	}
	_, err := o.client.AbortMultipartUpload(ctx, &oss.AbortMultipartUploadRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(upload.Key), UploadId: oss.Ptr(upload.ProviderUploadID),
	})
	if hasStorageErrorCode(err, "NoSuchUpload") {
		return nil
	}
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

func signedContentLength(headers map[string]string, size int64) map[string]string {
	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Content-Length"] = strconv.FormatInt(size, 10)
	return headers
}
