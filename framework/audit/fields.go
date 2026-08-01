package audit

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var (
	// ErrSensitiveField indicates that audit context contains a credential or
	// another field that must never be persisted.
	ErrSensitiveField = errors.New("audit context contains a sensitive field")
	// ErrInvalidSanitizedFields indicates that audit context is not a JSON object.
	ErrInvalidSanitizedFields = errors.New("audit context is not valid JSON")
	// ErrSanitizedFieldsTooLarge prevents audit context from becoming an
	// unbounded secondary copy of business data.
	ErrSanitizedFieldsTooLarge = errors.New("audit context exceeds the size limit")
)

const MaxSanitizedFieldsBytes = 64 << 10

// SanitizedFields is structured JSON context that is validated before it can
// be persisted. Values must already be minimized for audit use; validation
// rejects common credential, token, cookie, and signed-URL field names at any
// nesting depth.
type SanitizedFields map[string]any

// NewSanitizedFields validates and deep-copies a structured audit context map.
func NewSanitizedFields(input map[string]any) (SanitizedFields, error) {
	fields := SanitizedFields(input)
	raw, err := fields.validatedJSON()
	if err != nil {
		return nil, err
	}
	var copied SanitizedFields
	if err := json.Unmarshal(raw, &copied); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSanitizedFields, err)
	}
	return copied, nil
}

// Validate verifies JSON encoding and rejects sensitive field names.
func (f SanitizedFields) Validate() error {
	_, err := f.validatedJSON()
	return err
}

// Value implements driver.Valuer so GORM persists a validated JSON object.
func (f SanitizedFields) Value() (driver.Value, error) {
	raw, err := f.validatedJSON()
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// Scan implements sql.Scanner for SQLite TEXT, PostgreSQL JSONB, and MySQL JSON.
func (f *SanitizedFields) Scan(value any) error {
	if f == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidSanitizedFields)
	}
	var raw []byte
	switch typed := value.(type) {
	case nil:
		raw = []byte("{}")
	case []byte:
		raw = append([]byte(nil), typed...)
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("%w: unsupported database value %T", ErrInvalidSanitizedFields, value)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > MaxSanitizedFieldsBytes {
		return ErrSanitizedFieldsTooLarge
	}
	var decoded SanitizedFields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSanitizedFields, err)
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*f = decoded
	return nil
}

func (f SanitizedFields) validatedJSON() ([]byte, error) {
	if f == nil {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(map[string]any(f))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSanitizedFields, err)
	}
	if len(raw) > MaxSanitizedFieldsBytes {
		return nil, ErrSanitizedFieldsTooLarge
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSanitizedFields, err)
	}
	if err := validateFieldNames(normalized, ""); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateFieldNames(value any, parent string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			fieldPath := key
			if parent != "" {
				fieldPath = parent + "." + key
			}
			if sensitiveFieldName(key) {
				return fmt.Errorf("%w: %s", ErrSensitiveField, fieldPath)
			}
			if err := validateFieldNames(nested, fieldPath); err != nil {
				return err
			}
		}
	case []any:
		for index, nested := range typed {
			if err := validateFieldNames(nested, fmt.Sprintf("%s[%d]", parent, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sensitiveFieldName(name string) bool {
	var normalized strings.Builder
	for _, character := range strings.ToLower(name) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	value := normalized.String()
	for _, marker := range []string{
		"password",
		"passwd",
		"token",
		"secret",
		"cookie",
		"authorization",
		"credential",
		"apikey",
		"privatekey",
		"otp",
		"verificationcode",
		"signedurl",
		"signature",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
