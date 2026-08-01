package observability

import (
	"errors"
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const (
	MaxAttributes     = 32
	MaxAttributeKey   = 64
	MaxAttributeValue = 256
)

var (
	ErrInvalidAttributes = errors.New("observability: invalid attributes")
	attributeKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
)

// Attributes is an immutable, bounded set of low-cardinality string
// attributes. Values returns a defensive copy.
type Attributes struct {
	values map[string]string
}

// NewAttributes validates and copies values. Attribute keys are restricted to
// printable ASCII identifiers; values must be valid UTF-8 without control
// characters. These bounds reduce accidental high-cardinality or log-injection
// data, but callers remain responsible for excluding secrets and user content.
func NewAttributes(values map[string]string) (Attributes, error) {
	if err := validateAttributeValues(values); err != nil {
		return Attributes{}, err
	}
	return Attributes{values: cloneStringMap(values)}, nil
}

// Len returns the number of attributes.
func (a Attributes) Len() int {
	return len(a.values)
}

// Get returns one attribute without exposing the underlying map.
func (a Attributes) Get(key string) (string, bool) {
	value, ok := a.values[key]
	return value, ok
}

// Values returns a copy that callers may mutate freely.
func (a Attributes) Values() map[string]string {
	return cloneStringMap(a.values)
}

func (a Attributes) clone() Attributes {
	return Attributes{values: cloneStringMap(a.values)}
}

func (a Attributes) merge(updates Attributes) (Attributes, error) {
	merged := cloneStringMap(a.values)
	if merged == nil && updates.Len() > 0 {
		merged = make(map[string]string, updates.Len())
	}
	for key, value := range updates.values {
		merged[key] = value
	}
	return NewAttributes(merged)
}

func (a Attributes) validate() error {
	return validateAttributeValues(a.values)
}

func validateAttributeValues(values map[string]string) error {
	if len(values) > MaxAttributes {
		return fmt.Errorf(
			"%w: at most %d entries are allowed",
			ErrInvalidAttributes,
			MaxAttributes,
		)
	}
	for key, value := range values {
		if len(key) == 0 ||
			len(key) > MaxAttributeKey ||
			!attributeKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: invalid key %q", ErrInvalidAttributes, key)
		}
		if len(value) > MaxAttributeValue || !utf8.ValidString(value) {
			return fmt.Errorf(
				"%w: value for %q exceeds the UTF-8 boundary",
				ErrInvalidAttributes,
				key,
			)
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return fmt.Errorf(
					"%w: value for %q contains a control character",
					ErrInvalidAttributes,
					key,
				)
			}
		}
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
