package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
)

func TestFileUploadPolicySettingsExposeRuntimeAndPendingState(t *testing.T) {
	runtimePolicy := config.FileUploadPolicy{
		MaxUploadBytes:          10 << 20,
		ResumableUploadsEnabled: false,
	}
	fixture := newUploadPolicyAppFixture(t, runtimePolicy)

	initial, initialRecorder := getStorageSettingsForTest(t, fixture)
	if initialRecorder.Code != http.StatusOK {
		t.Fatalf("initial settings status = %d, body = %s", initialRecorder.Code, initialRecorder.Body.String())
	}
	if got := initialRecorder.Header().Get("ETag"); got != storageETag(fixture.installation.Revision) {
		t.Fatalf("initial ETag = %q, want %q", got, storageETag(fixture.installation.Revision))
	}
	if !initial.EnvironmentManaged {
		t.Fatal("fixture must prove upload policy updates remain available while storage is environment managed")
	}
	if initial.RestartRequired || initial.Revision != fixture.installation.Revision ||
		initial.RuntimeRevision != fixture.installation.Revision ||
		initial.RuntimeFileUploadPolicy != fileUploadPolicyResponseForTest(runtimePolicy) ||
		initial.PendingFileUploadPolicy != fileUploadPolicyResponseForTest(runtimePolicy) {
		t.Fatalf("initial storage settings = %#v", initial)
	}

	pendingPolicy := config.FileUploadPolicy{
		MaxUploadBytes:          64 << 20,
		ResumableUploadsEnabled: true,
	}
	updated, updateRecorder := putFileUploadPolicyForTest(
		t,
		fixture,
		storageETag(initial.Revision),
		"upload-policy-runtime-pending",
		pendingPolicy,
	)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if got := updateRecorder.Header().Get("ETag"); got != storageETag(initial.Revision+1) {
		t.Fatalf("updated ETag = %q, want %q", got, storageETag(initial.Revision+1))
	}
	if !updated.EnvironmentManaged || !updated.RestartRequired ||
		updated.Revision != initial.Revision+1 || updated.RuntimeRevision != initial.RuntimeRevision ||
		updated.RuntimeFileUploadPolicy != fileUploadPolicyResponseForTest(runtimePolicy) ||
		updated.PendingFileUploadPolicy != fileUploadPolicyResponseForTest(pendingPolicy) {
		t.Fatalf("updated storage settings = %#v", updated)
	}

	got, getRecorder := getStorageSettingsForTest(t, fixture)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("updated GET status = %d, body = %s", getRecorder.Code, getRecorder.Body.String())
	}
	if got != updated {
		t.Fatalf("GET settings = %#v, want update response %#v", got, updated)
	}
	if runtime := fixture.cfg.FileUploadRuntime(); runtime.MaxUploadBytes != runtimePolicy.MaxUploadBytes ||
		runtime.ResumableUploadsEnabled != runtimePolicy.ResumableUploadsEnabled ||
		runtime.LoadedRevision != initial.RuntimeRevision {
		t.Fatalf("immutable runtime policy changed before restart: %#v", runtime)
	}
}

func TestFileUploadPolicyUpdateValidatesMiBGranularityAndAbsoluteBounds(t *testing.T) {
	tests := []struct {
		name       string
		maxBytes   int64
		wantStatus int
	}{
		{name: "minimum one MiB", maxBytes: config.FileUploadSizeStepBytes, wantStatus: http.StatusOK},
		{name: "maximum one GiB", maxBytes: config.MaximumFileUploadBytes, wantStatus: http.StatusOK},
		{name: "below one MiB", maxBytes: config.FileUploadSizeStepBytes - 1, wantStatus: http.StatusBadRequest},
		{name: "not a one MiB increment", maxBytes: config.DefaultMaxUploadBytes + 1, wantStatus: http.StatusUnprocessableEntity},
		{name: "above one GiB", maxBytes: config.MaximumFileUploadBytes + config.FileUploadSizeStepBytes, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUploadPolicyAppFixture(t, config.DefaultFileUploadPolicy())
			before, err := config.ReadInstallation(fixture.installationPath)
			if err != nil {
				t.Fatal(err)
			}
			_, recorder := putFileUploadPolicyForTest(
				t,
				fixture,
				storageETag(before.Revision),
				"upload-policy-bound-"+strconv.FormatInt(test.maxBytes, 10),
				config.FileUploadPolicy{MaxUploadBytes: test.maxBytes, ResumableUploadsEnabled: true},
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			after, err := config.ReadInstallation(fixture.installationPath)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantStatus == http.StatusOK {
				if after.Revision != before.Revision+1 ||
					after.FileUploadPolicy.MaxUploadBytes != test.maxBytes ||
					!after.FileUploadPolicy.ResumableUploadsEnabled {
					t.Fatalf("saved installation = %#v", after)
				}
				return
			}
			if after.Revision != before.Revision || after.FileUploadPolicy != before.FileUploadPolicy {
				t.Fatalf("invalid update mutated installation: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestFileUploadPolicyUpdateRequiresCurrentIfMatch(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.DefaultFileUploadPolicy())
	policy := config.FileUploadPolicy{MaxUploadBytes: 32 << 20, ResumableUploadsEnabled: true}

	_, missing := putFileUploadPolicyForTest(t, fixture, "", "upload-policy-missing-if-match", policy)
	assertProblemCode(t, missing, http.StatusBadRequest, "REQUEST_INVALID")

	_, stale := putFileUploadPolicyForTest(
		t,
		fixture,
		storageETag(fixture.installation.Revision+1),
		"upload-policy-stale-if-match",
		policy,
	)
	assertProblemCode(t, stale, http.StatusConflict, "REQUEST_CONFLICT")

	after, err := config.ReadInstallation(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != fixture.installation.Revision ||
		after.FileUploadPolicy != fixture.installation.FileUploadPolicy {
		t.Fatalf("failed conditional writes mutated installation: %#v", after)
	}
}

func TestFileUploadPolicyUpdateIsIdempotentAndAudited(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.DefaultFileUploadPolicy())
	policy := config.FileUploadPolicy{MaxUploadBytes: 128 << 20, ResumableUploadsEnabled: true}
	etag := storageETag(fixture.installation.Revision)

	first, firstRecorder := putFileUploadPolicyForTest(
		t,
		fixture,
		etag,
		"upload-policy-idempotent",
		policy,
	)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
	}
	replayed, replayRecorder := putFileUploadPolicyForTest(
		t,
		fixture,
		etag,
		"upload-policy-idempotent",
		policy,
	)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body = %s", replayRecorder.Code, replayRecorder.Body.String())
	}
	if replayRecorder.Body.String() != firstRecorder.Body.String() ||
		replayRecorder.Header().Get("ETag") != firstRecorder.Header().Get("ETag") ||
		replayed != first {
		t.Fatalf("idempotent replay differs: first %#v / %q, replay %#v / %q",
			first, firstRecorder.Body.String(), replayed, replayRecorder.Body.String())
	}

	installation, err := config.ReadInstallation(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Revision != fixture.installation.Revision+1 || installation.FileUploadPolicy != policy {
		t.Fatalf("idempotent installation = %#v", installation)
	}
	var audits []domain.AuditLog
	if err := fixture.db.Where(
		"action = ? AND resource = ? AND resource_id = ?",
		"storage-settings:update-file-upload-policy",
		"storage-settings",
		"file-upload-policy",
	).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("policy audit count = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Result != "success" || audit.Source != "http" || audit.ActorID == nil ||
		audit.Before["revision"] != float64(fixture.installation.Revision) ||
		audit.After["revision"] != float64(installation.Revision) ||
		audit.After["maxUploadBytes"] != float64(policy.MaxUploadBytes) ||
		audit.After["resumableUploadsEnabled"] != policy.ResumableUploadsEnabled {
		t.Fatalf("policy audit = %#v", audit)
	}
}

func TestFileUploadPolicyUpdateRestoresInstallationWhenAuditFails(t *testing.T) {
	fixture := newUploadPolicyAppFixture(t, config.DefaultFileUploadPolicy())
	beforeBytes, err := os.ReadFile(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := config.ReadInstallation(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	_, recorder := putFileUploadPolicyForTest(
		t,
		fixture,
		storageETag(before.Revision),
		"upload-policy-audit-failure",
		config.FileUploadPolicy{MaxUploadBytes: 256 << 20, ResumableUploadsEnabled: true},
	)
	assertProblemCode(t, recorder, http.StatusInternalServerError, "INTERNAL_ERROR")

	afterBytes, err := os.ReadFile(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("installation document changed after audit failure:\nbefore: %s\nafter:  %s", beforeBytes, afterBytes)
	}
	after, err := config.ReadInstallation(fixture.installationPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.FileUploadPolicy != before.FileUploadPolicy {
		t.Fatalf("installation was not rolled back: before %#v, after %#v", before, after)
	}
}

func getStorageSettingsForTest(
	t *testing.T,
	fixture uploadPolicyAppFixture,
) (StorageSettingsResponse, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := serveRequest(
		fixture.server,
		fixture.cookie,
		http.MethodGet,
		"/api/v1/storage-settings",
		nil,
		"",
	)
	var response StorageSettingsResponse
	if recorder.Code >= 200 && recorder.Code < 300 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder
}

func putFileUploadPolicyForTest(
	t *testing.T,
	fixture uploadPolicyAppFixture,
	ifMatch string,
	idempotencyKey string,
	policy config.FileUploadPolicy,
) (StorageSettingsResponse, *httptest.ResponseRecorder) {
	t.Helper()
	body := mustJSONForTest(t, FileUploadPolicyUpdateRequest{
		MaxUploadBytes:          policy.MaxUploadBytes,
		ResumableUploadsEnabled: policy.ResumableUploadsEnabled,
	})
	request := authenticatedWriteRequest(
		fixture.cookie,
		http.MethodPut,
		"/api/v1/storage-settings/file-upload-policy",
		body,
	)
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	if idempotencyKey != "" {
		request.Header.Set(idempotencyHeader, idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, request)
	var response StorageSettingsResponse
	if recorder.Code >= 200 && recorder.Code < 300 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder
}

func fileUploadPolicyResponseForTest(policy config.FileUploadPolicy) FileUploadPolicyResponse {
	return FileUploadPolicyResponse{
		MaxUploadBytes:          policy.MaxUploadBytes,
		ResumableUploadsEnabled: policy.ResumableUploadsEnabled,
	}
}
