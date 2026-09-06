package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

type OSS struct {
	client     *oss.Client
	readClient *oss.Client
	httpClient oss.HTTPClient
	bucket     string
	policy     FilePolicy
}

func NewOSS(cfg *oss.Config, bucket string, policy FilePolicy) *OSS {
	return NewOSSWithReadConfig(cfg, cfg, bucket, policy)
}

func NewOSSWithReadConfig(
	cfg *oss.Config,
	readCfg *oss.Config,
	bucket string,
	policy FilePolicy,
) *OSS {
	configured := configuredOSS(cfg)
	configuredRead := configuredOSS(readCfg)
	httpClient := configured.HttpClient
	if httpClient == nil {
		httpClient = &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &OSS{
		client: oss.NewClient(&configured), readClient: oss.NewClient(&configuredRead),
		httpClient: httpClient, bucket: bucket, policy: policy,
	}
}

func configuredOSS(cfg *oss.Config) oss.Config {
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
	return configured
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

func (o *OSS) CheckBrowserUploadReadiness(
	ctx context.Context,
	origins []string,
) error {
	if len(origins) == 0 {
		return fmt.Errorf("%w: no web origin is configured", ErrBrowserUploadCORS)
	}
	probeBytes := make([]byte, 16)
	if _, err := rand.Read(probeBytes); err != nil {
		return fmt.Errorf("create CORS probe: %w", err)
	}
	signed, err := o.CreateUpload(ctx, UploadRequest{
		Key:     "aginex-readiness/" + hex.EncodeToString(probeBytes),
		Size:    1,
		Expires: time.Minute,
	})
	if err != nil {
		return err
	}
	headerNames := browserPreflightHeaderNames(signed.Headers)
	for _, candidate := range origins {
		origin := strings.TrimSpace(candidate)
		if origin == "" {
			return fmt.Errorf("%w: an empty web origin is configured", ErrBrowserUploadCORS)
		}
		request, requestErr := http.NewRequestWithContext(
			ctx,
			http.MethodOptions,
			signed.URL,
			nil,
		)
		if requestErr != nil {
			return fmt.Errorf("create CORS preflight: %w", requestErr)
		}
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", signed.Method)
		request.Header.Set("Access-Control-Request-Headers", strings.Join(headerNames, ","))
		response, requestErr := o.httpClient.Do(request)
		if requestErr != nil {
			return fmt.Errorf("%w: preflight request failed", ErrBrowserUploadCORS)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		closeErr := response.Body.Close()
		if closeErr != nil {
			return fmt.Errorf("%w: preflight response could not be closed", ErrBrowserUploadCORS)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices ||
			!corsOriginMatches(response.Header.Get("Access-Control-Allow-Origin"), origin) ||
			!headerTokenContains(response.Header.Get("Access-Control-Allow-Methods"), signed.Method) ||
			!corsHeadersAllow(response.Header.Get("Access-Control-Allow-Headers"), headerNames) ||
			!headerTokenContains(response.Header.Get("Access-Control-Expose-Headers"), "ETag") {
			return fmt.Errorf(
				"%w: preflight for configured origin returned status %d or incomplete headers",
				ErrBrowserUploadCORS,
				response.StatusCode,
			)
		}
	}
	return nil
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
	_, disposition, expires, err := controlledReadValues(request)
	if err != nil {
		return SignedRequest{}, err
	}
	result, err := o.readClient.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Key),
		ResponseContentDisposition: oss.Ptr(disposition),
	}, oss.PresignExpires(expires))
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: result.URL, Method: result.Method,
		Headers: result.SignedHeaders, ExpiresAt: result.Expiration,
	}, nil
}

func (o *OSS) FinalizeVerifiedPresentation(
	ctx context.Context,
	request VerifiedPresentationRequest,
) (ObjectInfo, error) {
	contentType, err := validateVerifiedPresentation(request)
	if err != nil {
		return ObjectInfo{}, err
	}
	ifMatch := strings.TrimSpace(request.ExpectedETag)
	if ifMatch != "" && !strings.HasPrefix(ifMatch, `"`) {
		ifMatch = `"` + strings.Trim(ifMatch, `"`) + `"`
	}
	copyRequest := &oss.CopyObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(request.Key),
		SourceBucket: oss.Ptr(o.bucket), SourceKey: oss.Ptr(request.Key),
		MetadataDirective: oss.Ptr("REPLACE"),
		ContentType:       oss.Ptr(contentType),
	}
	if ifMatch != "" {
		copyRequest.IfMatch = oss.Ptr(ifMatch)
	}
	if _, err := o.client.CopyObject(ctx, copyRequest); err != nil {
		return ObjectInfo{}, err
	}
	return o.Stat(ctx, request.Key)
}

func browserPreflightHeaderNames(headers map[string]string) []string {
	result := make([]string, 0, len(headers))
	for name := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		switch name {
		case "", "authorization", "content-length", "cookie", "host", "origin":
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func corsOriginMatches(allowed, origin string) bool {
	allowed = strings.TrimSpace(allowed)
	return allowed == "*" || strings.EqualFold(allowed, origin)
}

func headerTokenContains(value, expected string) bool {
	for _, candidate := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), expected) {
			return true
		}
	}
	return false
}

func corsHeadersAllow(value string, expected []string) bool {
	if strings.TrimSpace(value) == "*" {
		return true
	}
	for _, name := range expected {
		if !headerTokenContains(value, name) {
			return false
		}
	}
	return true
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
		ContentDisposition: oss.ToString(result.ContentDisposition),
		ETag:               strings.Trim(oss.ToString(result.ETag), `"`), ModifiedAt: oss.ToTime(result.LastModified),
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
