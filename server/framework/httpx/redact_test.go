package httpx

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactValueCoversCredentialsAndSignedURLs(t *testing.T) {
	sensitive := map[string]any{
		"password":          "correct horse battery staple",
		"access_token":      "access-token",
		"refreshToken":      "refresh-token",
		"Cookie":            "session=secret",
		"verification_code": "123456",
		"code":              "654321",
		"signed_url":        "https://storage.example/object?signature=secret",
		"authorization":     "Bearer secret",
	}
	for key, value := range sensitive {
		if got := RedactValue(key, value); got != RedactedValue {
			t.Fatalf("%s redacted value = %#v", key, got)
		}
	}

	signedURL := "https://bucket.example/object?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=secret"
	if got := RedactValue("url", signedURL); got != RedactedValue {
		t.Fatalf("signed URL redacted value = %#v", got)
	}
	if got := RedactValue("status_code", 200); got != 200 {
		t.Fatalf("status code was over-redacted: %#v", got)
	}
	if got := RedactValue(
		"error",
		errors.New("driver included password=secret"),
	); got != RedactedValue {
		t.Fatalf("error was not conservatively redacted: %#v", got)
	}
}

func TestRedactFieldsDoesNotMutateInput(t *testing.T) {
	input := map[string]any{"password": "secret", "user_id": "user-1"}
	output := RedactFields(input)
	if output["password"] != RedactedValue || output["user_id"] != "user-1" {
		t.Fatalf("redacted fields = %#v", output)
	}
	if input["password"] != "secret" {
		t.Fatalf("input was mutated: %#v", input)
	}
}

func TestRedactAttrCanBeUsedWithSlogReplaceAttr(t *testing.T) {
	attr := RedactAttr(nil, slog.String("access_token", "secret"))
	if got := attr.Value.String(); got != RedactedValue {
		t.Fatalf("redacted attr = %q", got)
	}
	plain := RedactAttr(nil, slog.String("request_id", "request-1"))
	if got := plain.Value.String(); got != "request-1" {
		t.Fatalf("plain attr = %q", got)
	}

	urlAttr := RedactAttr(nil, slog.String(
		"url",
		"https://storage.example/object?OSSAccessKeyId=key&Signature=secret",
	))
	if got := urlAttr.Value.String(); got != RedactedValue || strings.Contains(got, "secret") {
		t.Fatalf("URL attr = %q", got)
	}
	errorAttr := RedactAttr(
		nil,
		slog.Any("error", errors.New("password=secret")),
	)
	if got := errorAttr.Value.String(); got != RedactedValue {
		t.Fatalf("error attr = %q", got)
	}
}
