package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/framework/module"
	internalapp "github.com/xgtian-root/aginex/internal/app"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/multipartcleanup"
)

type workerTestModule struct {
	name     string
	register func(*module.Registry) error
}

func (item workerTestModule) Name() string {
	return item.name
}

func (item workerTestModule) Register(registry *module.Registry) error {
	return item.register(registry)
}

func TestComposeRegistryRegistersVersionedFileCleanupHandler(t *testing.T) {
	received := map[uint]string{}
	registry, err := composeRegistry(
		func(_ context.Context, payload json.RawMessage) error {
			received[fileCleanupJobVersionV1] = string(payload)
			return nil
		},
		func(_ context.Context, payload json.RawMessage) error {
			received[fileCleanupJobVersionV2] = string(payload)
			return nil
		},
		func(_ context.Context, payload json.RawMessage) error {
			received[fileCleanupJobVersionV3] = string(payload)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := jobs.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		version uint
		payload json.RawMessage
	}{
		{version: fileCleanupJobVersionV1, payload: json.RawMessage(`{"version":1}`)},
		{version: fileCleanupJobVersionV2, payload: json.RawMessage(`{"version":2}`)},
		{version: fileCleanupJobVersionV3, payload: json.RawMessage(`{"version":3}`)},
	}
	for _, test := range cases {
		err = dispatcher.Dispatch(context.Background(), jobs.Job{
			ID:          "job-1",
			Type:        fileCleanupJobType,
			Version:     test.version,
			Payload:     test.payload,
			State:       jobs.StateRunning,
			Attempts:    1,
			MaxAttempts: 10,
			CreatedBy:   authz.NewSystemActor("test-worker"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if received[test.version] != string(test.payload) {
			t.Fatalf("version %d payload = %q", test.version, received[test.version])
		}
	}
}

func TestMultipartCleanupRegistrationPreservesFileCleanupVersions(t *testing.T) {
	registry, err := composeRegistry(
		func(context.Context, json.RawMessage) error { return nil },
		func(context.Context, json.RawMessage) error { return nil },
		func(context.Context, json.RawMessage) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := registerMultipartCleanupHandler(
		registry,
		func(context.Context, json.RawMessage) error {
			called = true
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if handlers := registry.JobHandlers(); len(handlers) != 4 {
		t.Fatalf("registered handlers = %#v", handlers)
	}
	dispatcher, err := jobs.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(context.Background(), jobs.Job{
		ID: uuid.NewString(), Type: multipartcleanup.JobType,
		Version: multipartcleanup.PayloadVersion,
		Payload: json.RawMessage(`{"sessionId":"00000000-0000-4000-8000-000000000001","cause":"expiry"}`),
		State:   jobs.StateRunning, Attempts: 1, MaxAttempts: 10,
		CreatedBy: authz.NewSystemActor("test-worker"),
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("multipart cleanup handler was not dispatched")
	}
}

func TestComposeRegistryIncludesApplicationJobHandlers(t *testing.T) {
	called := false
	applicationModule := workerTestModule{
		name: "posta-jobs",
		register: func(registry *module.Registry) error {
			return registry.RegisterJobHandler(module.JobHandlerDefinition{
				Type:    "postmarks.reindex",
				Version: 1,
				Handle: func(context.Context, json.RawMessage) error {
					called = true
					return nil
				},
			})
		},
	}
	registry, err := composeRegistry(
		func(context.Context, json.RawMessage) error { return nil },
		func(context.Context, json.RawMessage) error { return nil },
		func(context.Context, json.RawMessage) error { return nil },
		applicationModule,
	)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := jobs.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(context.Background(), jobs.Job{
		ID:          "job-1",
		Type:        "postmarks.reindex",
		Version:     1,
		Payload:     json.RawMessage(`{"postmarkId":"example"}`),
		State:       jobs.StateRunning,
		Attempts:    1,
		MaxAttempts: 10,
		CreatedBy:   authz.NewSystemActor("test-worker"),
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("application job handler was not dispatched")
	}
}

func TestZeroBusinessWorkerDoesNotRegisterFileCleanup(t *testing.T) {
	registry, err := composeRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if handlers := registry.JobHandlers(); len(handlers) != 0 {
		t.Fatalf(
			"zero-business worker handlers = %#v, want none",
			handlers,
		)
	}

	hasFiles, err := modulesHaveResource(nil, "files")
	if err != nil {
		t.Fatal(err)
	}
	if hasFiles {
		t.Fatal("empty module composition unexpectedly enables files")
	}
	hasFiles, err = modulesHaveResource(
		[]module.Module{internalapp.FilesModule()},
		"files",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFiles {
		t.Fatal("official files module was not detected")
	}
}

func TestNewRejectsDisabledJobsBeforeAccessingDatabase(t *testing.T) {
	_, err := New(context.Background(), config.Config{
		Jobs: config.Jobs{Driver: "disabled"},
	}, nil)
	if !errors.Is(err, ErrJobsDisabled) {
		t.Fatalf("New error = %v, want ErrJobsDisabled", err)
	}
}

func TestNewRejectsNilContext(t *testing.T) {
	_, err := New(nil, config.Config{}, nil)
	if err == nil {
		t.Fatal("New accepted a nil context")
	}
}
