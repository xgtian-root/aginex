package database

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/observability"
	"gorm.io/gorm/logger"
)

func TestSafeLoggerNeverEmitsSQLParametersOrDatabaseErrors(t *testing.T) {
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, nil))
	databaseLogger := newSafeLogger(log).LogMode(logger.Info)
	secret := "TOP-SECRET-password-and-signed-url"

	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := recorder.Start(
		context.Background(),
		observability.SpanStart{
			Name: "test",
			Kind: observability.SpanKindServer,
		},
	)
	databaseLogger.Info(ctx, "diagnostic %s", secret)
	databaseLogger.Warn(ctx, "warning %s", secret)
	databaseLogger.Error(ctx, "error %s", secret)
	databaseLogger.Trace(
		ctx,
		time.Now().Add(-time.Second),
		func() (string, int64) {
			return "SELECT * FROM users WHERE password = '" + secret + "'", 1
		},
		errors.New(secret),
	)
	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})

	rendered := output.String()
	if strings.Contains(rendered, secret) ||
		strings.Contains(rendered, "SELECT *") ||
		strings.Contains(rendered, "password =") {
		t.Fatalf("safe database log leaked protected content: %s", rendered)
	}
	for _, expected := range []string{
		"Database diagnostic",
		"Database warning",
		"Database error",
		"Database operation failed",
		"trace_id",
		"span_id",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("safe database log is missing %q: %s", expected, rendered)
		}
	}
}
