package audit

import (
	"errors"
	"regexp"
	"strings"
)

const (
	ActorUser    = "user"
	ActorSystem  = "system"
	ActorService = "service"

	ResultSuccess = "success"
	ResultFailure = "failure"

	SourceHTTP   = "http"
	SourceWorker = "worker"
	SourceCLI    = "cli"
	SourceSystem = "system"
)

var (
	ErrActorKindRequired = errors.New("audit actor kind is required")
	ErrActionRequired    = errors.New("audit action is required")
	ErrResourceRequired  = errors.New("audit resource is required")
	ErrResultRequired    = errors.New("audit result is required")
	ErrSourceRequired    = errors.New("audit source is required")
	ErrActorKindInvalid  = errors.New("audit actor kind is invalid")
	ErrActionInvalid     = errors.New("audit action is invalid")
	ErrResourceInvalid   = errors.New("audit resource is invalid")
	ErrResultInvalid     = errors.New("audit result is invalid")
	ErrSourceInvalid     = errors.New("audit source is invalid")
	ErrMetadataTooLong   = errors.New("audit metadata is too long")
)

var (
	actionPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$`)
	resourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// Event is the provider-neutral audit record produced by a successful write.
// Before and After must contain already-sanitized values.
type Event struct {
	ActorID    *string
	ActorKind  string
	Action     string
	Resource   string
	ResourceID string
	Result     string
	RequestID  string
	Source     string
	Summary    string
	IPAddress  string
	Before     SanitizedFields
	After      SanitizedFields
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.ActorKind) == "" {
		return ErrActorKindRequired
	}
	if strings.TrimSpace(e.Action) == "" {
		return ErrActionRequired
	}
	if strings.TrimSpace(e.Resource) == "" {
		return ErrResourceRequired
	}
	if strings.TrimSpace(e.Result) == "" {
		return ErrResultRequired
	}
	if strings.TrimSpace(e.Source) == "" {
		return ErrSourceRequired
	}
	if e.ActorKind != ActorUser &&
		e.ActorKind != ActorSystem &&
		e.ActorKind != ActorService {
		return ErrActorKindInvalid
	}
	if e.Action != strings.TrimSpace(e.Action) ||
		len(e.Action) > 160 ||
		!actionPattern.MatchString(e.Action) {
		return ErrActionInvalid
	}
	if e.Resource != strings.TrimSpace(e.Resource) ||
		len(e.Resource) > 120 ||
		!resourcePattern.MatchString(e.Resource) {
		return ErrResourceInvalid
	}
	if e.Result != ResultSuccess && e.Result != ResultFailure {
		return ErrResultInvalid
	}
	if e.Source != SourceHTTP &&
		e.Source != SourceWorker &&
		e.Source != SourceCLI &&
		e.Source != SourceSystem {
		return ErrSourceInvalid
	}
	if (e.ActorID != nil && len(*e.ActorID) > 160) ||
		len(e.ResourceID) > 160 ||
		len(e.RequestID) > 64 ||
		len(e.IPAddress) > 64 ||
		len(e.Summary) > 4096 {
		return ErrMetadataTooLong
	}
	if err := e.Before.Validate(); err != nil {
		return err
	}
	if err := e.After.Validate(); err != nil {
		return err
	}
	return nil
}
