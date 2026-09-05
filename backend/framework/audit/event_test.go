package audit

import (
	"errors"
	"testing"
)

func TestEventValidate(t *testing.T) {
	valid := Event{
		ActorKind: ActorSystem,
		Action:    "products:create",
		Resource:  "product",
		Result:    ResultSuccess,
		Source:    SourceWorker,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}

	cases := []struct {
		name  string
		event Event
		want  error
	}{
		{
			name:  "actor kind",
			event: Event{Action: "products:create", Resource: "product", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrActorKindRequired,
		},
		{
			name:  "action",
			event: Event{ActorKind: ActorSystem, Resource: "product", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrActionRequired,
		},
		{
			name:  "resource",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrResourceRequired,
		},
		{
			name:  "result",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "product", Source: SourceHTTP},
			want:  ErrResultRequired,
		},
		{
			name:  "source",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "product", Result: ResultSuccess},
			want:  ErrSourceRequired,
		},
		{
			name:  "invalid actor kind",
			event: Event{ActorKind: "administrator", Action: "products:create", Resource: "product", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrActorKindInvalid,
		},
		{
			name:  "invalid action",
			event: Event{ActorKind: ActorSystem, Action: "Create Product", Resource: "product", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrActionInvalid,
		},
		{
			name:  "invalid resource",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "../product", Result: ResultSuccess, Source: SourceHTTP},
			want:  ErrResourceInvalid,
		},
		{
			name:  "invalid result",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "product", Result: "ok", Source: SourceHTTP},
			want:  ErrResultInvalid,
		},
		{
			name:  "invalid source",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "product", Result: ResultSuccess, Source: "browser"},
			want:  ErrSourceInvalid,
		},
		{
			name:  "request id too long",
			event: Event{ActorKind: ActorSystem, Action: "products:create", Resource: "product", Result: ResultSuccess, Source: SourceHTTP, RequestID: string(make([]byte, 65))},
			want:  ErrMetadataTooLong,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if err := item.event.Validate(); !errors.Is(err, item.want) {
				t.Fatalf("error = %v, want %v", err, item.want)
			}
		})
	}
}

func TestEventRejectsOversizedSanitizedFields(t *testing.T) {
	event := Event{
		ActorKind: ActorSystem,
		Action:    "system:test",
		Resource:  "test",
		Result:    ResultSuccess,
		Source:    SourceSystem,
		After: SanitizedFields{
			"payload": string(make([]byte, MaxSanitizedFieldsBytes)),
		},
	}
	if err := event.Validate(); !errors.Is(err, ErrSanitizedFieldsTooLarge) {
		t.Fatalf("error = %v, want ErrSanitizedFieldsTooLarge", err)
	}
}

func TestEventRejectsSensitiveAuditFields(t *testing.T) {
	event := Event{
		ActorKind: ActorUser,
		Action:    "users:update",
		Resource:  "user",
		Result:    ResultSuccess,
		Source:    SourceHTTP,
		After: SanitizedFields{
			"profile": map[string]any{
				"refreshToken": "must-not-be-recorded",
			},
		},
	}

	if err := event.Validate(); !errors.Is(err, ErrSensitiveField) {
		t.Fatalf("error = %v, want ErrSensitiveField", err)
	}
}

func TestNewSanitizedFieldsCopiesInput(t *testing.T) {
	input := map[string]any{
		"displayName": "Ada",
		"roles":       []any{"editor"},
	}
	fields, err := NewSanitizedFields(input)
	if err != nil {
		t.Fatal(err)
	}
	input["displayName"] = "Changed"
	input["roles"].([]any)[0] = "administrator"

	if got := fields["displayName"]; got != "Ada" {
		t.Fatalf("displayName = %#v, want Ada", got)
	}
	if got := fields["roles"].([]any)[0]; got != "editor" {
		t.Fatalf("role = %#v, want editor", got)
	}
}
