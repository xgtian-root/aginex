package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	frameworkidempotency "github.com/xgtian-root/aginex/backend/framework/idempotency"
	frameworkstorage "github.com/xgtian-root/aginex/backend/framework/storage"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	platformstorage "github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

func TestGenericFileUploadUsesServerDetectedPreviewSafety(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	tests := []struct {
		name            string
		filename        string
		browserType     string
		data            []byte
		wantPreviewKind string
		wantContentType string
	}{
		{
			name:            "pdf preview",
			filename:        "报价\".pdf",
			browserType:     "application/pdf",
			data:            validUploadPDFForTest(),
			wantPreviewKind: "pdf",
			wantContentType: "application/pdf",
		},
		{
			name:            "plain text download",
			filename:        "notes.txt",
			browserType:     "text/plain",
			data:            []byte("Aginex generic file upload\n"),
			wantPreviewKind: "none",
		},
		{
			name:            "html download",
			filename:        "unsafe.html",
			browserType:     "text/html",
			data:            []byte("<!doctype html><script>document.body.textContent='unsafe'</script>"),
			wantPreviewKind: "none",
		},
		{
			name:            "svg download",
			filename:        "unsafe.svg",
			browserType:     "image/svg+xml",
			data:            []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>"),
			wantPreviewKind: "none",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, intent := createUploadIntentForTest(t, server, cookie, map[string]any{
				"filename": test.filename, "contentType": test.browserType,
				"size": len(test.data), "visibility": "private",
			}, "")
			if intent.Code != http.StatusCreated {
				t.Fatalf("intent status = %d, body = %s", intent.Code, intent.Body.String())
			}
			assertSingleIntentForTest(t, prepared, int64(len(test.data)))
			uploadSingleIntentForTest(t, server, cookie, cfg, prepared, test.data)

			confirmed, confirmation := confirmUploadForTest(t, server, cookie, prepared.File.ID, "")
			if confirmation.Code != http.StatusOK {
				t.Fatalf("confirm status = %d, body = %s", confirmation.Code, confirmation.Body.String())
			}
			wantSHA := sha256.Sum256(test.data)
			if confirmed.Status != "ready" || confirmed.PreviewKind != test.wantPreviewKind ||
				confirmed.SHA256 != hex.EncodeToString(wantSHA[:]) || confirmed.Size != int64(len(test.data)) {
				t.Fatalf("confirmed file = %#v", confirmed)
			}
			if test.wantContentType != "" && confirmed.ContentType != test.wantContentType {
				t.Fatalf("content type = %q, want %q", confirmed.ContentType, test.wantContentType)
			}

			var persisted domain.FileObject
			if err := db.First(&persisted, "id = ?", confirmed.ID).Error; err != nil {
				t.Fatal(err)
			}
			info, err := server.store.Stat(t.Context(), persisted.ObjectKey)
			if err != nil {
				t.Fatal(err)
			}
			if info.ContentType != frameworkstorage.StoredContentType {
				t.Fatalf("stored object content type = %q", info.ContentType)
			}

			content := readFileContentForTest(t, server, cookie, cfg, confirmed.ID, "preview")
			if content.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("nosniff header = %q", content.Header().Get("X-Content-Type-Options"))
			}
			if !bytes.Equal(content.Body.Bytes(), test.data) {
				t.Fatal("downloaded bytes differ from uploaded bytes")
			}
			disposition := content.Header().Get("Content-Disposition")
			if !strings.Contains(disposition, "filename*=UTF-8''") {
				t.Fatalf("content disposition lacks filename*: %q", disposition)
			}
			if test.wantPreviewKind == "pdf" {
				if !strings.HasPrefix(disposition, "inline") ||
					content.Header().Get("Content-Type") != "application/pdf" ||
					content.Header().Get("Content-Security-Policy") != "sandbox" {
					t.Fatalf("PDF preview headers = %#v", content.Header())
				}
				download := readFileContentForTest(t, server, cookie, cfg, confirmed.ID, "download")
				if !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") ||
					download.Header().Get("Content-Type") != frameworkstorage.StoredContentType {
					t.Fatalf("forced PDF download headers = %#v", download.Header())
				}
			} else if !strings.HasPrefix(disposition, "attachment") ||
				content.Header().Get("Content-Type") != frameworkstorage.StoredContentType ||
				content.Header().Get("Content-Security-Policy") != "" {
				t.Fatalf("non-preview response headers = %#v", content.Header())
			}
		})
	}
}

func validUploadPDFForTest() []byte {
	return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\nxref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\nstartxref\n45\n%%EOF\n")
}

func TestLocalSingleUploadCannotOverwriteAfterConcurrentConfirmation(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.DefaultFileUploadPolicy())
	first := []byte("first-published-payload")
	second := []byte("other-untrusted-payload")
	if len(first) != len(second) {
		t.Fatal("concurrency fixture payload sizes differ")
	}
	prepared, intent := createUploadIntentForTest(t, fixture.server, fixture.cookie, map[string]any{
		"filename": "race.bin", "contentType": "application/octet-stream",
		"size": len(first), "visibility": "private",
	}, "")
	if intent.Code != http.StatusCreated || prepared.Upload == nil {
		t.Fatalf("intent status/payload = %d %#v", intent.Code, prepared)
	}

	release := make(chan struct{})
	blocked := &gatedUploadReader{
		data: second, started: make(chan struct{}), release: release,
	}
	path := strings.TrimPrefix(prepared.Upload.URL, fixture.cfg.HTTP.PublicURL)
	request := httptest.NewRequest(http.MethodPut, path, blocked)
	request.ContentLength = int64(len(second))
	request.Header.Set("Content-Type", frameworkstorage.StoredContentType)
	request.AddCookie(fixture.cookie)
	addTestCSRF(request)
	secondResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		fixture.server.Handler().ServeHTTP(recorder, request)
		secondResult <- recorder
	}()
	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("second upload did not begin reading")
	}

	uploadSingleIntentForTest(t, fixture.server, fixture.cookie, fixture.cfg, prepared, first)
	confirmed, confirmation := confirmUploadForTest(t, fixture.server, fixture.cookie, prepared.File.ID, "race-confirm")
	if confirmation.Code != http.StatusOK {
		t.Fatalf("confirmation = %d, body = %s", confirmation.Code, confirmation.Body.String())
	}
	close(release)
	select {
	case recorder := <-secondResult:
		if recorder.Code < 400 {
			t.Fatalf("late concurrent upload status = %d, want rejection", recorder.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late concurrent upload did not finish")
	}

	wantSHA := fmt.Sprintf("%x", sha256.Sum256(first))
	if confirmed.SHA256 != wantSHA {
		t.Fatalf("confirmed SHA-256 = %q, want %q", confirmed.SHA256, wantSHA)
	}
	var stored domain.FileObject
	if err := fixture.db.First(&stored, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	reader, err := fixture.server.store.Open(context.Background(), stored.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, first) {
		t.Fatalf("stored content = %q, want first payload", content)
	}
}

type gatedUploadReader struct {
	data      []byte
	offset    int
	started   chan struct{}
	release   <-chan struct{}
	announced bool
}

func (reader *gatedUploadReader) Read(buffer []byte) (int, error) {
	if !reader.announced {
		reader.announced = true
		close(reader.started)
		<-reader.release
	}
	if reader.offset == len(reader.data) {
		return 0, io.EOF
	}
	written := copy(buffer, reader.data[reader.offset:])
	reader.offset += written
	return written, nil
}

func TestUploadIntentPolicyAndStrategyConditions(t *testing.T) {
	t.Run("runtime maximum returns stable 413", func(t *testing.T) {
		_, _, server, cookie := newFileHandlerTestApp(t)
		_, response := createUploadIntentForTest(t, server, cookie, map[string]any{
			"filename": "too-large.bin", "contentType": "application/octet-stream",
			"size": config.DefaultMaxUploadBytes + 1, "visibility": "private",
		}, "")
		assertProblemCode(t, response, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE")
	})

	t.Run("disabled resumable defaults large files to single", func(t *testing.T) {
		fixture := newUploadPolicyAppFixture(t, config.FileUploadPolicy{
			MaxUploadBytes: 64 << 20, ResumableUploadsEnabled: false,
		})
		body := map[string]any{
			"filename": "large.bin", "contentType": "application/octet-stream",
			"size": multipartThresholdBytes + 1, "visibility": "private",
			"resumeFingerprint": strings.Repeat("a", 64),
		}
		_, disabled := createUploadIntentForTest(t, fixture.server, fixture.cookie, withStrategy(body, "resumable"), "")
		assertProblemCode(t, disabled, http.StatusConflict, "RESUMABLE_UPLOADS_DISABLED")
		prepared, single := createUploadIntentForTest(t, fixture.server, fixture.cookie, body, "")
		if single.Code != http.StatusCreated {
			t.Fatalf("single intent status = %d, body = %s", single.Code, single.Body.String())
		}
		assertSingleIntentForTest(t, prepared, multipartThresholdBytes+1)
	})

	t.Run("threshold is strict greater than 32 MiB", func(t *testing.T) {
		fixture := newUploadPolicyAppFixture(t, config.FileUploadPolicy{
			MaxUploadBytes: 64 << 20, ResumableUploadsEnabled: true,
		})
		body := map[string]any{
			"filename": "boundary.bin", "contentType": "application/octet-stream",
			"size": multipartThresholdBytes, "visibility": "private",
			"resumeFingerprint": strings.Repeat("b", 64),
		}
		_, rejected := createUploadIntentForTest(t, fixture.server, fixture.cookie, withStrategy(body, "resumable"), "")
		assertProblemCode(t, rejected, http.StatusUnprocessableEntity, "RESUMABLE_THRESHOLD_NOT_MET")
		prepared, single := createUploadIntentForTest(t, fixture.server, fixture.cookie, body, "")
		if single.Code != http.StatusCreated {
			t.Fatalf("boundary single intent status = %d, body = %s", single.Code, single.Body.String())
		}
		assertSingleIntentForTest(t, prepared, multipartThresholdBytes)
	})

	t.Run("provider must expose multipart capability", func(t *testing.T) {
		fixture := newUploadPolicyAppFixture(t, config.FileUploadPolicy{
			MaxUploadBytes: 64 << 20, ResumableUploadsEnabled: true,
		})
		fixture.server.store = singleOnlyStorage{Storage: fixture.server.store}
		policy := serveRequest(fixture.server, fixture.cookie, http.MethodGet, "/api/v1/files/upload-policy", nil, "")
		if policy.Code != http.StatusOK {
			t.Fatalf("policy status = %d, body = %s", policy.Code, policy.Body.String())
		}
		var current UploadPolicyResponse
		if err := json.Unmarshal(policy.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if !current.ResumableUploadsEnabled || current.ResumableAvailable {
			t.Fatalf("upload policy = %#v", current)
		}
		_, response := createUploadIntentForTest(t, fixture.server, fixture.cookie, map[string]any{
			"filename": "unsupported.bin", "contentType": "application/octet-stream",
			"size": multipartThresholdBytes + 1, "visibility": "private",
			"strategy": "resumable", "resumeFingerprint": strings.Repeat("c", 64),
		}, "")
		assertProblemCode(t, response, http.StatusUnprocessableEntity, "RESUMABLE_PROVIDER_UNSUPPORTED")
	})
}

func TestLocalSingleUploadRequiresValidUnexpiredIntentURL(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	data := []byte("signed local upload")
	prepared, intent := createUploadIntentForTest(t, server, cookie, map[string]any{
		"filename": "signed.txt", "contentType": "text/plain",
		"size": len(data), "visibility": "private",
	}, "")
	if intent.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", intent.Code, intent.Body.String())
	}
	assertSingleIntentForTest(t, prepared, int64(len(data)))

	parsed, err := url.Parse(prepared.Upload.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("expires") == "" || parsed.Query().Get("signature") == "" {
		t.Fatalf("local upload URL is unsigned: %s", prepared.Upload.URL)
	}
	invalid := *parsed
	invalidQuery := invalid.Query()
	invalidQuery.Set("signature", strings.Repeat("0", 64))
	invalid.RawQuery = invalidQuery.Encode()
	invalidResponse := serveRequest(server, cookie, http.MethodPut, invalid.RequestURI(), data, frameworkstorage.StoredContentType)
	assertProblemCode(t, invalidResponse, http.StatusForbidden, "UPLOAD_AUTHORIZATION_INVALID")

	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().UTC().Add(-time.Minute).Unix()
	expired := *parsed
	expiredQuery := expired.Query()
	expiredQuery.Set("expires", strconv.FormatInt(expiredAt, 10))
	expiredQuery.Set("signature", server.localSingleUploadSignature(file, expiredAt))
	expired.RawQuery = expiredQuery.Encode()
	expiredResponse := serveRequest(server, cookie, http.MethodPut, expired.RequestURI(), data, frameworkstorage.StoredContentType)
	assertProblemCode(t, expiredResponse, http.StatusGone, "UPLOAD_AUTHORIZATION_EXPIRED")

	uploadSingleIntentForTest(t, server, cookie, cfg, prepared, data)
	confirmed, confirmation := confirmUploadForTest(t, server, cookie, prepared.File.ID, "")
	if confirmation.Code != http.StatusOK || confirmed.Status != "ready" {
		t.Fatalf("valid signed upload confirmation = %d %#v, body = %s", confirmation.Code, confirmed, confirmation.Body.String())
	}
}

func TestPersistedUploadsContinueAfterPolicyIsLowered(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.FileUploadPolicy{
		MaxUploadBytes: 64 << 20, ResumableUploadsEnabled: true,
	})

	singleBytes := bytes.Repeat([]byte("s"), 2<<20)
	single, singleIntent := createUploadIntentForTest(t, fixture.server, fixture.cookie, map[string]any{
		"filename": "existing-single.txt", "contentType": "text/plain",
		"size": len(singleBytes), "visibility": "private",
	}, "existing-single-intent")
	if singleIntent.Code != http.StatusCreated {
		t.Fatalf("single intent status = %d, body = %s", singleIntent.Code, singleIntent.Body.String())
	}
	assertSingleIntentForTest(t, single, int64(len(singleBytes)))

	partOne := bytes.Repeat([]byte("A"), int(multipartPartSizeBytes))
	partTwo := bytes.Repeat([]byte("B"), 4096)
	totalSize := int64(len(partOne) + len(partTwo))
	fingerprint := strings.Repeat("d", 64)
	resumable, resumableIntent := createUploadIntentForTest(t, fixture.server, fixture.cookie, map[string]any{
		"filename": "existing-resumable.bin", "contentType": "application/octet-stream",
		"size": totalSize, "visibility": "private", "strategy": "resumable",
		"resumeFingerprint": fingerprint,
	}, "existing-resumable-intent")
	if resumableIntent.Code != http.StatusCreated {
		t.Fatalf("resumable intent status = %d, body = %s", resumableIntent.Code, resumableIntent.Body.String())
	}
	if resumable.Strategy != "resumable" || resumable.Upload != nil || resumable.Session == nil ||
		resumable.Session.PartCount != 2 || resumable.Session.PartSize != multipartPartSizeBytes {
		t.Fatalf("resumable intent = %#v", resumable)
	}

	var persistedSession domain.FileUploadSession
	if err := fixture.db.First(&persistedSession, "id = ?", resumable.Session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedSession.ProviderUploadID == "" {
		t.Fatal("provider upload ID was not persisted")
	}
	assertBodyOmitsSecrets(t, resumableIntent, persistedSession.ProviderUploadID)

	signed := signUploadPartsForTest(t, fixture.server, fixture.cookie, resumable.Session.ID, 1, 2)
	if len(signed.Items) != 2 {
		t.Fatalf("signed parts = %#v", signed)
	}
	etagOne := uploadSignedPartForTest(t, fixture.server, fixture.cookie, fixture.cfg, signed.Items[0], partOne)
	browserETagOne := `"` + etagOne + `"`
	ackOne := ackUploadPartsForTest(t, fixture.server, fixture.cookie, resumable.Session.ID, "ack-part-one", AckUploadPartRequest{
		PartNumber: 1, ETag: browserETagOne,
	})
	if ackOne.Code != http.StatusOK {
		t.Fatalf("ack part one status = %d, body = %s", ackOne.Code, ackOne.Body.String())
	}
	var persistedPart domain.FileUploadPart
	if err := fixture.db.First(&persistedPart, "session_id = ? AND part_number = ?", resumable.Session.ID, 1).Error; err != nil {
		t.Fatal(err)
	}
	if persistedPart.ETag != browserETagOne {
		t.Fatalf("persisted ETag = %q, want browser UploadPart value %q", persistedPart.ETag, browserETagOne)
	}
	etagTwoBeforeRestart := uploadSignedPartForTest(t, fixture.server, fixture.cookie, fixture.cfg, signed.Items[1], partTwo)
	assertBodyOmitsSecrets(t, ackOne, persistedSession.ProviderUploadID, etagOne, etagTwoBeforeRestart)

	updated, err := config.UpdateInstallationFileUploadPolicy(
		fixture.installationPath,
		fixture.installation.Revision,
		config.FileUploadPolicy{MaxUploadBytes: 1 << 20, ResumableUploadsEnabled: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.installation = updated
	restartedConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(restartedConfig, fixture.db)
	if err != nil {
		t.Fatal(err)
	}

	uploadSingleIntentForTest(t, restarted, fixture.cookie, restartedConfig, single, singleBytes)
	confirmedSingle, singleConfirmation := confirmUploadForTest(t, restarted, fixture.cookie, single.File.ID, "existing-single-confirm")
	if singleConfirmation.Code != http.StatusOK || confirmedSingle.Status != "ready" || len(confirmedSingle.SHA256) != 64 {
		t.Fatalf("existing single confirmation = %d %#v, body = %s", singleConfirmation.Code, confirmedSingle, singleConfirmation.Body.String())
	}

	wrongResume := resumeUploadForTest(t, restarted, fixture.cookie, resumable.Session.ID, strings.Repeat("e", 64), "")
	assertProblemCode(t, wrongResume, http.StatusConflict, "UPLOAD_FINGERPRINT_MISMATCH")
	resumedRecorder := resumeUploadForTest(t, restarted, fixture.cookie, resumable.Session.ID, fingerprint, "resume-after-restart")
	if resumedRecorder.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body = %s", resumedRecorder.Code, resumedRecorder.Body.String())
	}
	var resumed UploadSessionResponse
	if err := json.Unmarshal(resumedRecorder.Body.Bytes(), &resumed); err != nil {
		t.Fatal(err)
	}
	if len(resumed.CompletedParts) != 1 || resumed.CompletedParts[0].PartNumber != 1 {
		t.Fatalf("resumed completed parts = %#v; unacknowledged part must be retried", resumed.CompletedParts)
	}
	assertBodyOmitsSecrets(t, resumedRecorder, persistedSession.ProviderUploadID, etagOne, etagTwoBeforeRestart)

	resigned := signUploadPartsForTest(t, restarted, fixture.cookie, resumable.Session.ID, 2)
	if len(resigned.Items) != 1 || resigned.Items[0].PartNumber != 2 {
		t.Fatalf("re-signed parts = %#v", resigned)
	}
	etagTwo := uploadSignedPartForTest(t, restarted, fixture.cookie, restartedConfig, resigned.Items[0], partTwo)
	ackTwo := ackUploadPartsForTest(t, restarted, fixture.cookie, resumable.Session.ID, "ack-part-two", AckUploadPartRequest{
		PartNumber: 2, ETag: etagTwo,
	})
	if ackTwo.Code != http.StatusOK {
		t.Fatalf("ack part two status = %d, body = %s", ackTwo.Code, ackTwo.Body.String())
	}

	completed, completeRecorder := completeUploadSessionForTest(t, restarted, fixture.cookie, resumable.Session.ID, "complete-after-restart")
	if completeRecorder.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeRecorder.Code, completeRecorder.Body.String())
	}
	digest := sha256.New()
	_, _ = digest.Write(partOne)
	_, _ = digest.Write(partTwo)
	if completed.Status != "ready" || completed.Size != totalSize ||
		completed.SHA256 != hex.EncodeToString(digest.Sum(nil)) || completed.PreviewKind != "none" {
		t.Fatalf("completed resumable file = %#v", completed)
	}
	assertNoDurableUploadSecretLeak(t, fixture.db, persistedSession.ProviderUploadID, etagOne, etagTwo)

	_, rejectedSingle := createUploadIntentForTest(t, restarted, fixture.cookie, map[string]any{
		"filename": "new-too-large.txt", "contentType": "text/plain",
		"size": len(singleBytes), "visibility": "private",
	}, "")
	assertProblemCode(t, rejectedSingle, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE")
	_, rejectedResumable := createUploadIntentForTest(t, restarted, fixture.cookie, map[string]any{
		"filename": "new-too-large.bin", "contentType": "application/octet-stream",
		"size": totalSize, "visibility": "private", "strategy": "resumable",
		"resumeFingerprint": fingerprint,
	}, "")
	assertProblemCode(t, rejectedResumable, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE")
}

func TestResumableSessionCancelAndOwnerIsolation(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.FileUploadPolicy{
		MaxUploadBytes: 64 << 20, ResumableUploadsEnabled: true,
	})
	const passwordValue = "correct horse battery staple"
	createFileUser(t, fixture.db, "upload-a@example.com", passwordValue, "own")
	createFileUser(t, fixture.db, "upload-b@example.com", passwordValue, "own")
	userA := loginCookieAs(t, fixture.server, "upload-a@example.com", passwordValue)
	userB := loginCookieAs(t, fixture.server, "upload-b@example.com", passwordValue)
	fingerprint := strings.Repeat("f", 64)
	prepared, intent := createUploadIntentForTest(t, fixture.server, userB, map[string]any{
		"filename": "cancel.bin", "contentType": "application/octet-stream",
		"size": multipartThresholdBytes + 1, "visibility": "private",
		"strategy": "resumable", "resumeFingerprint": fingerprint,
	}, "")
	if intent.Code != http.StatusCreated || prepared.Session == nil {
		t.Fatalf("resumable intent status/payload = %d %#v, body = %s", intent.Code, prepared, intent.Body.String())
	}

	for _, request := range []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodGet, path: "/api/v1/files/upload-sessions/" + prepared.Session.ID},
		{method: http.MethodPost, path: "/api/v1/files/upload-sessions/" + prepared.Session.ID + "/resume", body: mustJSONForTest(t, ResumeUploadSessionRequest{Fingerprint: fingerprint})},
		{method: http.MethodDelete, path: "/api/v1/files/upload-sessions/" + prepared.Session.ID},
	} {
		response := serveRequest(fixture.server, userA, request.method, request.path, request.body, contentTypeForBody(request.body))
		if response.Code != http.StatusNotFound {
			t.Fatalf("cross-owner %s %s status = %d, body = %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	deleteWhileUploading := serveRequest(
		fixture.server,
		userB,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	assertProblemCode(t, deleteWhileUploading, http.StatusConflict, "FILE_UPLOAD_IN_PROGRESS")

	cancel := serveRequest(fixture.server, userB, http.MethodDelete, "/api/v1/files/upload-sessions/"+prepared.Session.ID, nil, "")
	if cancel.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, body = %s", cancel.Code, cancel.Body.String())
	}
	var session domain.FileUploadSession
	if err := fixture.db.First(&session, "id = ?", prepared.Session.ID).Error; err != nil {
		t.Fatal(err)
	}
	var file domain.FileObject
	if err := fixture.db.First(&file, "id = ?", session.FileID).Error; err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.FileUploadSessionStatusCancelled || file.Status != "deleted" {
		t.Fatalf("cancelled session/file = %#v / %#v", session, file)
	}
	assertBodyOmitsSecrets(t, intent, session.ProviderUploadID)

	completing, completingIntent := createUploadIntentForTest(t, fixture.server, userB, map[string]any{
		"filename": "completing.bin", "contentType": "application/octet-stream",
		"size": multipartThresholdBytes + 1, "visibility": "private",
		"strategy": "resumable", "resumeFingerprint": strings.Repeat("c", 64),
	}, "")
	if completingIntent.Code != http.StatusCreated || completing.Session == nil {
		t.Fatalf("completing fixture = %d %#v", completingIntent.Code, completing)
	}
	if err := fixture.db.Model(&domain.FileUploadSession{}).
		Where("id = ?", completing.Session.ID).
		Update("status", domain.FileUploadSessionStatusCompleting).Error; err != nil {
		t.Fatal(err)
	}
	cancelCompleting := serveRequest(
		fixture.server,
		userB,
		http.MethodDelete,
		"/api/v1/files/upload-sessions/"+completing.Session.ID,
		nil,
		"",
	)
	assertProblemCode(t, cancelCompleting, http.StatusConflict, "UPLOAD_SESSION_STATE_CONFLICT")
	var completingSession domain.FileUploadSession
	if err := fixture.db.First(&completingSession, "id = ?", completing.Session.ID).Error; err != nil {
		t.Fatal(err)
	}
	multipart, ok := platformstorage.AsMultipart(fixture.server.store)
	if !ok {
		t.Fatal("local store does not expose multipart")
	}
	var completingFile domain.FileObject
	if err := fixture.db.First(&completingFile, "id = ?", completingSession.FileID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := multipart.ListUploadedParts(context.Background(), frameworkstorage.MultipartUpload{
		Key: completingFile.ObjectKey, ProviderUploadID: completingSession.ProviderUploadID,
	}); err != nil {
		t.Fatalf("cancelling a completing session aborted provider state: %v", err)
	}
}

type uploadPolicyAppFixture struct {
	cfg              config.Config
	db               *gorm.DB
	server           *App
	cookie           *http.Cookie
	installationPath string
	installation     config.Installation
}

func newUploadPolicyAppFixture(t *testing.T, policy config.FileUploadPolicy) uploadPolicyAppFixture {
	t.Helper()
	temporary := t.TempDir()
	installationPath := filepath.Join(temporary, "config", "aginex.json")
	databasePath := filepath.Join(temporary, "aginex.db")
	storageRoot := filepath.Join(temporary, "uploads")
	secret, err := config.GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := config.NewManagedInstallationWithStorage(
		config.Database{Driver: "sqlite", DSN: databasePath},
		secret,
		config.Storage{Driver: "local", LocalRoot: storageRoot},
	)
	if err != nil {
		t.Fatal(err)
	}
	installation.FileUploadPolicy = policy
	if err := config.CommitInstallation(installationPath, installation); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGINEX_CONFIG_FILE", installationPath)
	t.Setenv("AGINEX_ENV", "test")
	t.Setenv("AGINEX_DATABASE_DRIVER", "")
	t.Setenv("AGINEX_DATABASE_DSN", "")
	t.Setenv("AGINEX_SESSION_SECRET", "")
	t.Setenv("AGINEX_SESSION_SECURE", "false")
	t.Setenv("AGINEX_API_PUBLIC_URL", "http://aginex.test")
	t.Setenv("AGINEX_WEB_ORIGINS", "http://localhost:3000")
	t.Setenv("AGINEX_STORAGE_DRIVER", "local")
	t.Setenv("AGINEX_STORAGE_LOCAL_ROOT", storageRoot)
	t.Setenv("AGINEX_STORAGE_BUCKET", "")
	t.Setenv("AGINEX_STORAGE_REGION", "")
	t.Setenv("AGINEX_STORAGE_ENDPOINT", "")
	t.Setenv("AGINEX_STORAGE_ACCESS_KEY_ID", "")
	t.Setenv("AGINEX_STORAGE_ACCESS_KEY_SECRET", "")
	t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD", "correct horse battery staple")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return uploadPolicyAppFixture{
		cfg: cfg, db: db, server: server, cookie: loginCookie(t, server),
		installationPath: installationPath, installation: installation,
	}
}

type singleOnlyStorage struct {
	platformstorage.Storage
}

func withStrategy(input map[string]any, strategy string) map[string]any {
	copy := make(map[string]any, len(input)+1)
	for key, value := range input {
		copy[key] = value
	}
	copy["strategy"] = strategy
	return copy
}

func createUploadIntentForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	input map[string]any,
	idempotencyKey string,
) (UploadIntentResponse, *httptest.ResponseRecorder) {
	t.Helper()
	body := mustJSONForTest(t, input)
	var recorder *httptest.ResponseRecorder
	if idempotencyKey == "" {
		recorder = serveRequest(server, cookie, http.MethodPost, "/api/v1/files/upload-intents", body, "application/json")
	} else {
		recorder = serveIdempotentRequest(server, cookie, http.MethodPost, "/api/v1/files/upload-intents", body, idempotencyKey)
	}
	var response UploadIntentResponse
	if recorder.Code >= 200 && recorder.Code < 300 && len(recorder.Body.Bytes()) > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder
}

func assertSingleIntentForTest(t *testing.T, prepared UploadIntentResponse, size int64) {
	t.Helper()
	if prepared.Strategy != "single" || prepared.Upload == nil || prepared.Session != nil {
		t.Fatalf("single upload intent = %#v", prepared)
	}
	if prepared.Upload.Method != http.MethodPut ||
		prepared.Upload.Headers["Content-Type"] != frameworkstorage.StoredContentType ||
		prepared.Upload.Headers["Content-Length"] != strconv.FormatInt(size, 10) {
		t.Fatalf("single upload authorization = %#v", prepared.Upload)
	}
	if strings.Contains(prepared.Upload.URL, prepared.File.OriginalName) {
		t.Fatalf("opaque upload URL contains filename: %s", prepared.Upload.URL)
	}
}

func uploadSingleIntentForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	cfg config.Config,
	prepared UploadIntentResponse,
	data []byte,
) {
	t.Helper()
	if prepared.Upload == nil {
		t.Fatal("single upload intent omitted upload authorization")
	}
	path := strings.TrimPrefix(prepared.Upload.URL, cfg.HTTP.PublicURL)
	response := serveRequest(server, cookie, http.MethodPut, path, data, frameworkstorage.StoredContentType)
	if response.Code != http.StatusNoContent {
		t.Fatalf("single upload status = %d, body = %s", response.Code, response.Body.String())
	}
}

func confirmUploadForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	fileID string,
	idempotencyKey string,
) (FileResponse, *httptest.ResponseRecorder) {
	t.Helper()
	path := "/api/v1/files/" + fileID + "/confirm"
	var recorder *httptest.ResponseRecorder
	if idempotencyKey == "" {
		recorder = serveRequest(server, cookie, http.MethodPost, path, nil, "")
	} else {
		recorder = serveIdempotentRequest(server, cookie, http.MethodPost, path, nil, idempotencyKey)
	}
	var response FileResponse
	if recorder.Code >= 200 && recorder.Code < 300 && len(recorder.Body.Bytes()) > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder
}

func readFileContentForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	cfg config.Config,
	fileID string,
	purpose string,
) *httptest.ResponseRecorder {
	t.Helper()
	urlResponse := serveRequest(server, cookie, http.MethodGet, "/api/v1/files/"+fileID+"/url?purpose="+purpose, nil, "")
	if urlResponse.Code != http.StatusOK {
		t.Fatalf("file URL status = %d, body = %s", urlResponse.Code, urlResponse.Body.String())
	}
	var signed SignedRequestResponse
	if err := json.Unmarshal(urlResponse.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(signed.URL, cfg.HTTP.PublicURL)
	content := serveRequest(server, cookie, http.MethodGet, path, nil, "")
	if content.Code != http.StatusOK {
		t.Fatalf("file content status = %d, body = %s", content.Code, content.Body.String())
	}
	return content
}

func signUploadPartsForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	sessionID string,
	partNumbers ...int,
) SignUploadPartsResponse {
	t.Helper()
	body := mustJSONForTest(t, SignUploadPartsRequest{PartNumbers: partNumbers})
	recorder := serveRequest(server, cookie, http.MethodPost, "/api/v1/files/upload-sessions/"+sessionID+"/parts/sign", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("sign parts status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response SignUploadPartsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func uploadSignedPartForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	cfg config.Config,
	part SignedUploadPartResponse,
	data []byte,
) string {
	t.Helper()
	if int64(len(data)) != part.Size || part.Upload.Method != http.MethodPut ||
		part.Upload.Headers["Content-Type"] != frameworkstorage.StoredContentType {
		t.Fatalf("signed part/data = %#v / %d bytes", part, len(data))
	}
	path := strings.TrimPrefix(part.Upload.URL, cfg.HTTP.PublicURL)
	recorder := serveRequest(server, cookie, http.MethodPut, path, data, frameworkstorage.StoredContentType)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("part %d upload status = %d, body = %s", part.PartNumber, recorder.Code, recorder.Body.String())
	}
	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("part %d upload omitted ETag", part.PartNumber)
	}
	return etag
}

func ackUploadPartsForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	sessionID string,
	idempotencyKey string,
	parts ...AckUploadPartRequest,
) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSONForTest(t, AckUploadPartsRequest{Parts: parts})
	path := "/api/v1/files/upload-sessions/" + sessionID + "/parts/ack"
	if idempotencyKey == "" {
		return serveRequest(server, cookie, http.MethodPost, path, body, "application/json")
	}
	return serveIdempotentRequest(server, cookie, http.MethodPost, path, body, idempotencyKey)
}

func resumeUploadForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	sessionID string,
	fingerprint string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSONForTest(t, ResumeUploadSessionRequest{Fingerprint: fingerprint})
	path := "/api/v1/files/upload-sessions/" + sessionID + "/resume"
	if idempotencyKey == "" {
		return serveRequest(server, cookie, http.MethodPost, path, body, "application/json")
	}
	return serveIdempotentRequest(server, cookie, http.MethodPost, path, body, idempotencyKey)
}

func completeUploadSessionForTest(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	sessionID string,
	idempotencyKey string,
) (FileResponse, *httptest.ResponseRecorder) {
	t.Helper()
	path := "/api/v1/files/upload-sessions/" + sessionID + "/complete"
	var recorder *httptest.ResponseRecorder
	if idempotencyKey == "" {
		recorder = serveRequest(server, cookie, http.MethodPost, path, nil, "")
	} else {
		recorder = serveIdempotentRequest(server, cookie, http.MethodPost, path, nil, idempotencyKey)
	}
	var response FileResponse
	if recorder.Code >= 200 && recorder.Code < 300 && len(recorder.Body.Bytes()) > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder
}

func assertNoDurableUploadSecretLeak(t *testing.T, db *gorm.DB, secrets ...string) {
	t.Helper()
	var audits []domain.AuditLog
	if err := db.Order("created_at").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	auditJSON := string(mustJSONForTest(t, audits))
	var replayBodies []struct {
		ResponseBody []byte `gorm:"column:response_body"`
	}
	if err := db.Table(frameworkidempotency.TableName).Select("response_body").Find(&replayBodies).Error; err != nil {
		t.Fatal(err)
	}
	var persistedReplay strings.Builder
	for _, record := range replayBodies {
		persistedReplay.Write(record.ResponseBody)
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(auditJSON, secret) {
			t.Fatalf("audit payload leaked upload secret %q", secret)
		}
		if strings.Contains(persistedReplay.String(), secret) {
			t.Fatalf("idempotency response leaked upload secret %q", secret)
		}
	}
}

func assertBodyOmitsSecrets(t *testing.T, recorder *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("response body leaked upload secret %q: %s", secret, recorder.Body.String())
		}
	}
}

func contentTypeForBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return "application/json"
}

func mustJSONForTest(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
