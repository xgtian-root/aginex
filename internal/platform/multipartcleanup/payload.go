package multipartcleanup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/jobs"
)

const (
	JobType        = "storage.multipart.cleanup"
	PayloadVersion = uint(1)
)

var ErrInvalidPayload = errors.New("multipart cleanup: invalid payload")

// Cause identifies the terminal state a cleanup delivery is allowed to
// produce. It deliberately excludes provider details and credentials.
type Cause string

const (
	CauseExpiry Cause = "expiry"
	CauseCancel Cause = "cancel"
)

// Payload is the exact storage.multipart.cleanup version-one contract.
// Provider upload IDs are resolved from protected database state and never
// enter the durable jobs table.
type Payload struct {
	SessionID string `json:"sessionId"`
	Cause     Cause  `json:"cause"`
}

// NewEnqueueRequest builds the credential-free durable cleanup request shared
// by intent expiry and explicit cancellation paths.
func NewEnqueueRequest(
	sessionID string,
	cause Cause,
	scheduledAt time.Time,
	actor authz.Actor,
	trace jobs.TraceContext,
) (jobs.EnqueueRequest, error) {
	payload := Payload{SessionID: sessionID, Cause: cause}
	if err := payload.Validate(); err != nil {
		return jobs.EnqueueRequest{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return jobs.EnqueueRequest{}, err
	}
	return jobs.EnqueueRequest{
		Type:           JobType,
		Version:        PayloadVersion,
		Payload:        raw,
		IdempotencyKey: "multipart-session:" + sessionID + ":" + string(cause),
		ScheduledAt:    scheduledAt,
		MaxAttempts:    10,
		CreatedBy:      actor,
		Trace:          trace,
	}, nil
}

func (payload Payload) Validate() error {
	sessionID, err := uuid.Parse(payload.SessionID)
	if err != nil || sessionID.String() != payload.SessionID {
		return ErrInvalidPayload
	}
	if payload.Cause != CauseExpiry && payload.Cause != CauseCancel {
		return ErrInvalidPayload
	}
	return nil
}

func decodePayload(raw json.RawMessage) (Payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Payload{}, fmt.Errorf("%w: trailing content", ErrInvalidPayload)
	}
	if err := payload.Validate(); err != nil {
		return Payload{}, err
	}
	return payload, nil
}
