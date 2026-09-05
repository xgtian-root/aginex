package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const localMultipartDirectory = ".aginex-multipart"

type Local struct {
	root          string
	uploadBaseURL string
	readBaseURL   string
	policy        FilePolicy
}

type localMultipartMetadata struct {
	Key          string `json:"key"`
	ExpectedSize int64  `json:"expectedSize"`
}

// LocalMultipartStaging is non-credential maintenance metadata for a Local
// multipart directory. It is used only by the bounded orphan repair scanner.
type LocalMultipartStaging struct {
	ProviderUploadID string
	ModifiedAt       time.Time
}

func NewLocal(root, uploadBaseURL, readBaseURL string, policy FilePolicy) (*Local, error) {
	if root == "" {
		return nil, errors.New("local storage root is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, localMultipartDirectory), 0o750); err != nil {
		return nil, err
	}
	return &Local{
		root: absolute, uploadBaseURL: uploadBaseURL, readBaseURL: readBaseURL, policy: policy,
	}, nil
}

func (l *Local) CreateUpload(_ context.Context, request UploadRequest) (SignedRequest, error) {
	if err := validateUpload(l.policy, request); err != nil {
		return SignedRequest{}, err
	}
	if _, err := l.path(request.Key); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	return SignedRequest{
		URL: l.objectURL(l.uploadBaseURL, request.Key), Method: http.MethodPut,
		Headers: map[string]string{
			"Content-Type":   StoredContentType,
			"Content-Length": strconv.FormatInt(request.Size, 10),
		},
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (l *Local) CheckReadiness(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local storage readiness context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	probe, err := os.CreateTemp(l.root, ".aginex-readiness-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	return errors.Join(closeErr, removeErr)
}

func (l *Local) SignRead(ctx context.Context, key string, expires time.Duration) (SignedRequest, error) {
	return l.SignControlledRead(ctx, ControlledReadRequest{
		Key: key, Expires: expires, ContentType: StoredContentType,
		Disposition: ReadDispositionAttachment, Filename: "download",
	})
}

// SignControlledRead carries only a validated purpose to the authenticated
// local-content handler. That handler must derive MIME type and filename from
// persisted verification metadata and must set X-Content-Type-Options: nosniff;
// query values are routing intent, never trusted response headers.
func (l *Local) SignControlledRead(
	_ context.Context,
	request ControlledReadRequest,
) (SignedRequest, error) {
	_, _, expires, err := controlledReadValues(request)
	if err != nil {
		return SignedRequest{}, err
	}
	readURL, err := url.Parse(l.objectURL(l.readBaseURL, request.Key))
	if err != nil {
		return SignedRequest{}, err
	}
	query := readURL.Query()
	if request.Disposition == ReadDispositionInline {
		query.Set("purpose", "preview")
	} else {
		query.Set("purpose", "download")
	}
	readURL.RawQuery = query.Encode()
	return SignedRequest{
		URL: readURL.String(), Method: http.MethodGet, Headers: map[string]string{},
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (l *Local) Stat(_ context.Context, key string) (ObjectInfo, error) {
	objectPath, err := l.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := os.Stat(objectPath)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key: key, Size: info.Size(), ContentType: StoredContentType, ModifiedAt: info.ModTime().UTC(),
	}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	objectPath, err := l.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(objectPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Put streams one exact-size object to a private temporary file before an
// atomic no-replace publish. It never buffers the complete file in memory and
// a second authorized request can never overwrite bytes already published for
// the same intent.
func (l *Local) Put(
	ctx context.Context,
	key string,
	body io.Reader,
	expectedSize int64,
) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	if err := validateExistingTransferSize(expectedSize); err != nil {
		return ObjectInfo{}, err
	}
	objectPath, err := l.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	digest := sha256.New()
	if _, err := writeExactAtomic(ctx, objectPath, body, expectedSize, digest, false); err != nil {
		return ObjectInfo{}, err
	}
	info, err := l.Stat(ctx, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.ETag = hex.EncodeToString(digest.Sum(nil))
	return info, nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	objectPath, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(objectPath)
}

func (l *Local) InitiateMultipart(
	ctx context.Context,
	request UploadRequest,
) (MultipartUpload, error) {
	if err := validateUpload(l.policy, request); err != nil {
		return MultipartUpload{}, err
	}
	if _, err := l.path(request.Key); err != nil {
		return MultipartUpload{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MultipartUpload{}, err
	}
	upload := MultipartUpload{Key: request.Key, ProviderUploadID: uuid.NewString()}
	directory, err := l.multipartPath(upload)
	if err != nil {
		return MultipartUpload{}, err
	}
	if err := os.Mkdir(directory, 0o750); err != nil {
		return MultipartUpload{}, err
	}
	if err := syncDirectory(filepath.Dir(directory)); err != nil {
		_ = os.RemoveAll(directory)
		return MultipartUpload{}, err
	}
	metadata, err := json.Marshal(localMultipartMetadata{Key: request.Key, ExpectedSize: request.Size})
	if err != nil {
		_ = os.RemoveAll(directory)
		return MultipartUpload{}, err
	}
	if _, err := writeExactAtomic(
		ctx,
		filepath.Join(directory, "metadata.json"),
		bytes.NewReader(metadata),
		int64(len(metadata)),
		nil,
		false,
	); err != nil {
		_ = os.RemoveAll(directory)
		return MultipartUpload{}, err
	}
	return upload, nil
}

func (l *Local) SignUploadPart(
	_ context.Context,
	request MultipartPartRequest,
) (SignedRequest, error) {
	if err := validateMultipartPart(l.policy, request); err != nil {
		return SignedRequest{}, err
	}
	if _, err := l.loadMultipartMetadata(request.Upload); err != nil {
		return SignedRequest{}, err
	}
	expires := normalizedExpiry(request.Expires)
	partURL, err := url.Parse(l.objectURL(l.uploadBaseURL, request.Upload.Key))
	if err != nil {
		return SignedRequest{}, err
	}
	query := partURL.Query()
	query.Set("uploadId", request.Upload.ProviderUploadID)
	query.Set("partNumber", strconv.FormatInt(int64(request.PartNumber), 10))
	partURL.RawQuery = query.Encode()
	return SignedRequest{
		URL: partURL.String(), Method: http.MethodPut,
		Headers: map[string]string{
			"Content-Type":   StoredContentType,
			"Content-Length": strconv.FormatInt(request.Size, 10),
		},
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

// PutMultipartPart is the local HTTP adapter's streaming equivalent of a
// provider UploadPart request.
func (l *Local) PutMultipartPart(
	ctx context.Context,
	request MultipartPartRequest,
	body io.Reader,
) (UploadedPart, error) {
	if err := validateMultipartPart(l.policy, request); err != nil {
		return UploadedPart{}, err
	}
	directory, metadata, err := l.multipartDirectory(request.Upload)
	if err != nil {
		return UploadedPart{}, err
	}
	if request.Size > metadata.ExpectedSize {
		return UploadedPart{}, ErrInvalidMultipart
	}
	digest := sha256.New()
	partPath := filepath.Join(directory, localPartName(request.PartNumber))
	if _, err := writeExactAtomic(ctx, partPath, body, request.Size, digest, true); err != nil {
		return UploadedPart{}, err
	}
	return UploadedPart{
		PartNumber: request.PartNumber,
		Size:       request.Size,
		ETag:       hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func (l *Local) ListUploadedParts(
	ctx context.Context,
	upload MultipartUpload,
) ([]UploadedPart, error) {
	directory, _, err := l.multipartDirectory(upload)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	parts := make([]UploadedPart, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		numberValue := strings.TrimSuffix(entry.Name(), ".part")
		number, err := strconv.ParseInt(numberValue, 10, 32)
		if err != nil || number < 1 || number > 10_000 {
			continue
		}
		part, err := inspectLocalPart(ctx, filepath.Join(directory, entry.Name()), int32(number))
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	return parts, nil
}

func (l *Local) CompleteMultipart(
	ctx context.Context,
	request MultipartCompleteRequest,
) (ObjectInfo, error) {
	if err := validateCompletion(request); err != nil {
		return ObjectInfo{}, err
	}
	if err := validateCompletionSize(l.policy, request.Parts); err != nil {
		return ObjectInfo{}, err
	}
	directory, metadata, err := l.multipartDirectory(request.Upload)
	if err != nil {
		if errors.Is(err, ErrMultipartNotFound) {
			if info, statErr := l.Stat(ctx, request.Upload.Key); statErr == nil {
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}

	var total int64
	for _, part := range request.Parts {
		total += part.Size
	}
	if total != metadata.ExpectedSize {
		return ObjectInfo{}, ErrInvalidMultipart
	}

	objectPath, err := l.path(request.Upload.Key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o750); err != nil {
		return ObjectInfo{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(objectPath), ".aginex-complete-*")
	if err != nil {
		return ObjectInfo{}, err
	}
	temporaryName := temporary.Name()
	completed := false
	defer func() {
		_ = temporary.Close()
		if !completed {
			_ = os.Remove(temporaryName)
		}
	}()
	digest := sha256.New()
	for _, acknowledged := range request.Parts {
		part, err := os.Open(filepath.Join(directory, localPartName(acknowledged.PartNumber)))
		if err != nil {
			return ObjectInfo{}, err
		}
		partDigest := sha256.New()
		copied, copyErr := io.Copy(
			io.MultiWriter(temporary, digest, partDigest),
			io.LimitReader(storageContextReader{ctx: ctx, reader: part}, acknowledged.Size+1),
		)
		closeErr := part.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return ObjectInfo{}, err
		}
		// Size and digest are derived from the same open descriptor that was
		// copied. A concurrent atomic part replacement can affect a later part
		// upload, but can never create a check-then-open manifest bypass here.
		if copied != acknowledged.Size ||
			hex.EncodeToString(partDigest.Sum(nil)) != normalizeMultipartETag(acknowledged.ETag) {
			return ObjectInfo{}, ErrInvalidMultipart
		}
	}
	if err := temporary.Sync(); err != nil {
		return ObjectInfo{}, err
	}
	if err := temporary.Close(); err != nil {
		return ObjectInfo{}, err
	}
	if err := publishAtomic(temporaryName, objectPath, false); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := l.Stat(ctx, request.Upload.Key)
			if statErr == nil && info.Size == total {
				if removeErr := os.RemoveAll(directory); removeErr != nil {
					return ObjectInfo{}, fmt.Errorf("remove completed multipart staging: %w", removeErr)
				}
				return info, nil
			}
		}
		return ObjectInfo{}, err
	}
	completed = true
	if err := os.RemoveAll(directory); err != nil {
		return ObjectInfo{}, fmt.Errorf("remove completed multipart staging: %w", err)
	}
	info, err := l.Stat(ctx, request.Upload.Key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.ETag = hex.EncodeToString(digest.Sum(nil))
	return info, nil
}

func (l *Local) AbortMultipart(_ context.Context, upload MultipartUpload) error {
	directory, _, err := l.multipartDirectory(upload)
	if errors.Is(err, ErrMultipartNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(directory)
}

// ListMultipartStaging returns at most limit old, canonical staging
// directories. Symlinks and unexpected entries are never followed.
func (l *Local) ListMultipartStaging(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]LocalMultipartStaging, error) {
	if l == nil || limit < 1 || limit > 100 {
		return nil, ErrInvalidMultipart
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := os.Open(filepath.Join(l.root, localMultipartDirectory))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	result := make([]LocalMultipartStaging, 0, limit)
	inspected := 0
	for len(result) < limit && inspected < limit*10 {
		entries, readErr := directory.ReadDir(min(64, limit*10-inspected))
		for _, entry := range entries {
			inspected++
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			parsed, parseErr := uuid.Parse(entry.Name())
			if parseErr != nil || parsed.String() != entry.Name() || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return nil, infoErr
			}
			modified := info.ModTime().UTC()
			if !modified.Before(before) {
				continue
			}
			result = append(result, LocalMultipartStaging{
				ProviderUploadID: entry.Name(), ModifiedAt: modified,
			})
			if len(result) == limit {
				break
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return result, nil
}

// RemoveMultipartStaging removes one exact canonical Local staging directory.
// Callers must first prove no persisted upload session owns the upload ID.
func (l *Local) RemoveMultipartStaging(ctx context.Context, providerUploadID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parsed, err := uuid.Parse(providerUploadID)
	if err != nil || parsed.String() != providerUploadID {
		return ErrInvalidMultipart
	}
	directory := filepath.Join(l.root, localMultipartDirectory, providerUploadID)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInvalidMultipart
	}
	return os.RemoveAll(directory)
}

func (l *Local) path(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	if key == localMultipartDirectory || strings.HasPrefix(key, localMultipartDirectory+"/") {
		return "", ErrInvalidKey
	}
	objectPath := filepath.Join(l.root, filepath.FromSlash(key))
	relative, err := filepath.Rel(l.root, objectPath)
	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return "", ErrInvalidKey
	}
	return objectPath, nil
}

func (l *Local) multipartPath(upload MultipartUpload) (string, error) {
	if err := validateMultipartUpload(upload); err != nil {
		return "", err
	}
	parsed, err := uuid.Parse(upload.ProviderUploadID)
	if err != nil || parsed.String() != upload.ProviderUploadID {
		return "", ErrInvalidMultipart
	}
	return filepath.Join(l.root, localMultipartDirectory, upload.ProviderUploadID), nil
}

func (l *Local) multipartDirectory(upload MultipartUpload) (string, localMultipartMetadata, error) {
	directory, err := l.multipartPath(upload)
	if err != nil {
		return "", localMultipartMetadata{}, err
	}
	metadata, err := l.loadMultipartMetadata(upload)
	if err != nil {
		return "", localMultipartMetadata{}, err
	}
	return directory, metadata, nil
}

func (l *Local) loadMultipartMetadata(upload MultipartUpload) (localMultipartMetadata, error) {
	directory, err := l.multipartPath(upload)
	if err != nil {
		return localMultipartMetadata{}, err
	}
	data, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if errors.Is(err, os.ErrNotExist) {
		return localMultipartMetadata{}, ErrMultipartNotFound
	}
	if err != nil {
		return localMultipartMetadata{}, err
	}
	var metadata localMultipartMetadata
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Key != upload.Key || metadata.ExpectedSize < 1 {
		return localMultipartMetadata{}, ErrInvalidMultipart
	}
	return metadata, nil
}

func (l *Local) objectURL(baseURL, key string) string {
	return fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), url.PathEscape(key))
}

func normalizedExpiry(expires time.Duration) time.Duration {
	if expires <= 0 {
		return 10 * time.Minute
	}
	return expires
}

func localPartName(partNumber int32) string {
	return fmt.Sprintf("%05d.part", partNumber)
}

func writeExactAtomic(
	ctx context.Context,
	destination string,
	reader io.Reader,
	expectedSize int64,
	digest hash.Hash,
	replace bool,
) (int64, error) {
	if reader == nil || expectedSize < 1 {
		return 0, ErrInvalidMultipart
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return 0, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".aginex-upload-*")
	if err != nil {
		return 0, err
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	writers := []io.Writer{temporary}
	if digest != nil {
		writers = append(writers, digest)
	}
	written, copyErr := io.Copy(
		io.MultiWriter(writers...),
		io.LimitReader(storageContextReader{ctx: ctx, reader: reader}, expectedSize+1),
	)
	if copyErr != nil {
		return written, copyErr
	}
	if written != expectedSize {
		return written, fmt.Errorf(
			"%w: expected %d bytes, received at least %d",
			ErrFileSizeMismatch, expectedSize, written,
		)
	}
	if err := temporary.Sync(); err != nil {
		return written, err
	}
	if err := temporary.Close(); err != nil {
		return written, err
	}
	if err := publishAtomic(temporaryName, destination, replace); err != nil {
		return written, err
	}
	committed = true
	return written, nil
}

func publishAtomic(source, destination string, replace bool) error {
	if replace {
		if err := os.Rename(source, destination); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(destination))
	}
	// The temporary file is created beside destination, so a hard link is an
	// atomic O_EXCL-style publish of the already-fsynced inode. Unlike Rename,
	// Link fails with os.ErrExist and never replaces a verified object.
	if err := os.Link(source, destination); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("remove published temporary file: %w", err)
	}
	return syncDirectory(filepath.Dir(destination))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func inspectLocalPart(ctx context.Context, partPath string, partNumber int32) (UploadedPart, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	file, err := os.Open(partPath)
	if errors.Is(err, os.ErrNotExist) {
		return UploadedPart{}, ErrMultipartNotFound
	}
	if err != nil {
		return UploadedPart{}, err
	}
	digest := sha256.New()
	size, copyErr := io.Copy(digest, storageContextReader{ctx: ctx, reader: file})
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return UploadedPart{}, err
	}
	return UploadedPart{
		PartNumber: partNumber,
		Size:       size,
		ETag:       hex.EncodeToString(digest.Sum(nil)),
	}, nil
}
