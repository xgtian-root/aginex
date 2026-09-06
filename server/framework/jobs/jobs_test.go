package jobs

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/authz"
)

func TestEnqueueRequestNormalize(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	request := EnqueueRequest{
		Type:           " storage.cleanup ",
		Version:        1,
		Payload:        json.RawMessage(`{"objectKey":"uploads/a.png"}`),
		IdempotencyKey: " file-1 ",
		CreatedBy:      authz.NewSystemActor("api"),
	}
	if err := request.Normalize(now); err != nil {
		t.Fatal(err)
	}
	if request.Type != "storage.cleanup" || request.IdempotencyKey != "file-1" {
		t.Fatalf("normalized request = %#v", request)
	}
	if !request.ScheduledAt.Equal(now) || request.MaxAttempts != 10 {
		t.Fatalf("defaults = scheduled %s attempts %d", request.ScheduledAt, request.MaxAttempts)
	}
}

func TestEnqueueRequestRejectsInvalidMetadata(t *testing.T) {
	cases := []EnqueueRequest{
		{Type: "Storage Cleanup", Version: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: "a", CreatedBy: authz.NewSystemActor("api")},
		{Type: "storage.cleanup", Payload: json.RawMessage(`{}`), IdempotencyKey: "a", CreatedBy: authz.NewSystemActor("api")},
		{Type: "storage.cleanup", Version: 1, Payload: json.RawMessage(`{`), IdempotencyKey: "a", CreatedBy: authz.NewSystemActor("api")},
		{Type: "storage.cleanup", Version: 1, Payload: json.RawMessage(`{}`), CreatedBy: authz.NewSystemActor("api")},
		{Type: "storage.cleanup", Version: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: "a"},
	}
	for index := range cases {
		if err := cases[index].Normalize(time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d error = %v, want ErrInvalid", index, err)
		}
	}
}

func TestRetryDelayUsesCappedExponentialBackoffAndJitter(t *testing.T) {
	if got := RetryDelay(1, time.Second, time.Minute, 0); got != time.Second {
		t.Fatalf("attempt 1 = %s", got)
	}
	if got := RetryDelay(4, time.Second, time.Minute, 0); got != 8*time.Second {
		t.Fatalf("attempt 4 = %s", got)
	}
	if got := RetryDelay(100, time.Second, time.Minute, 0); got != time.Minute {
		t.Fatalf("capped delay = %s", got)
	}
	if got := RetryDelay(2, 10*time.Second, time.Minute, -1); got != 16*time.Second {
		t.Fatalf("negative jitter delay = %s", got)
	}
	if got := RetryDelay(2, 10*time.Second, time.Minute, 1); got != 24*time.Second {
		t.Fatalf("positive jitter delay = %s", got)
	}
}

func TestNormalizeListDefaultsToDeadAndValidatesFilters(t *testing.T) {
	request, err := NormalizeList(ListRequest{Type: " storage.cleanup "})
	if err != nil {
		t.Fatal(err)
	}
	if request.State != StateDead ||
		request.Type != "storage.cleanup" ||
		request.Page != 1 ||
		request.PageSize != 20 {
		t.Fatalf("normalized list request = %#v", request)
	}

	for index, invalid := range []ListRequest{
		{State: "unknown"},
		{Type: "Storage Cleanup"},
		{Page: -1},
		{Page: 1_000_001},
		{PageSize: 101},
	} {
		if _, err := NormalizeList(invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d error = %v, want ErrInvalid", index, err)
		}
	}
}

func TestNormalizeJobID(t *testing.T) {
	const canonical = "e4f2cc53-08aa-46ae-8b63-80a8df8d5b69"
	got, err := NormalizeJobID(" E4F2CC53-08AA-46AE-8B63-80A8DF8D5B69 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != canonical {
		t.Fatalf("normalized job ID = %q, want %q", got, canonical)
	}
	for _, invalid := range []string{"", "job-1", "00000000-0000-0000-0000-000000000000"} {
		if _, err := NormalizeJobID(invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("NormalizeJobID(%q) error = %v, want ErrInvalid", invalid, err)
		}
	}
}
