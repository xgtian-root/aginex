package services

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/framework/observability"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"gorm.io/gorm"
)

type testStore struct{}

func (*testStore) CreateUpload(
	context.Context,
	frameworkstorage.UploadRequest,
) (frameworkstorage.SignedRequest, error) {
	return frameworkstorage.SignedRequest{}, nil
}

func (*testStore) SignRead(
	context.Context,
	string,
	time.Duration,
) (frameworkstorage.SignedRequest, error) {
	return frameworkstorage.SignedRequest{}, nil
}

func (*testStore) Stat(
	context.Context,
	string,
) (frameworkstorage.ObjectInfo, error) {
	return frameworkstorage.ObjectInfo{}, nil
}

func (*testStore) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

func (*testStore) Delete(context.Context, string) error {
	return nil
}

func TestRuntimeContextIsExplicitAndValidated(t *testing.T) {
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "runtime.db")),
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(
		database,
		nil,
		&testStore{},
		nil,
		recorder,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := ContextWithRuntime(t.Context(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := RuntimeFromContext(ctx)
	if !ok ||
		resolved.Database == nil ||
		resolved.Storage == nil ||
		resolved.Writes != nil ||
		resolved.Jobs != nil ||
		resolved.Observability != recorder {
		t.Fatalf("runtime = %#v, found = %t", resolved, ok)
	}

	var nilStore *testStore
	if _, err := NewRuntime(
		database,
		nil,
		nilStore,
		nil,
		recorder,
	); err == nil {
		t.Fatal("NewRuntime accepted typed nil storage")
	}
	if _, err := NewRuntime(
		database,
		nil,
		&testStore{},
		nil,
		nil,
	); err == nil {
		t.Fatal("NewRuntime accepted nil observability")
	}
	if _, ok := RuntimeFromContext(context.Background()); ok {
		t.Fatal("empty context unexpectedly contained runtime services")
	}
}

type nilUnitOfWork struct{}

func (*nilUnitOfWork) Run(
	context.Context,
	func(*gorm.DB) (frameworkaudit.Event, error),
) error {
	return nil
}

type nilQueue struct{}

func (*nilQueue) Bind(*gorm.DB) (jobs.Queue, error) {
	return nil, nil
}

func (*nilQueue) Enqueue(
	context.Context,
	jobs.EnqueueRequest,
) (jobs.EnqueueResult, error) {
	return jobs.EnqueueResult{}, nil
}

func (*nilQueue) Claim(
	context.Context,
	jobs.ClaimRequest,
) ([]jobs.Job, error) {
	return nil, nil
}

func (*nilQueue) Heartbeat(context.Context, string, string) error {
	return nil
}

func (*nilQueue) Succeed(context.Context, string, string) error {
	return nil
}

func (*nilQueue) Fail(
	context.Context,
	string,
	string,
	error,
) (jobs.State, error) {
	return "", nil
}

func (*nilQueue) RetryDead(context.Context, string) error {
	return nil
}

func TestRuntimeRejectsTypedNilOptionalDependencies(t *testing.T) {
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "runtime-nil.db")),
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	var writes *nilUnitOfWork
	if _, err := NewRuntime(
		database,
		writes,
		&testStore{},
		nil,
		recorder,
	); err == nil {
		t.Fatal("NewRuntime accepted a typed-nil unit of work")
	}
	var queue *nilQueue
	if _, err := NewRuntime(
		database,
		nil,
		&testStore{},
		queue,
		recorder,
	); err == nil {
		t.Fatal("NewRuntime accepted a typed-nil job queue")
	}
}
