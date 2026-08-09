package database

import (
	"context"
	"errors"
	"testing"

	"github.com/xgtian-root/aginex/internal/config"
)

func TestOpenContextRejectsCanceledStartupBeforeOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := OpenContext(ctx, config.Database{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/must-not-open.db",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext error = %v, want context cancellation", err)
	}
}
