package httpx

import (
	"regexp"
	"strings"
	"testing"
)

var traceParentPattern = regexp.MustCompile(
	`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`,
)

func TestTraceParentPreservesValidVersionZeroContext(t *testing.T) {
	input := "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01"
	got := TraceParent(input)
	want := strings.ToLower(input)
	if got != want {
		t.Fatalf("TraceParent() = %q, want %q", got, want)
	}
}

func TestTraceParentReplacesInvalidOrUnsupportedInput(t *testing.T) {
	inputs := []string{
		"",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra",
		strings.Repeat("a", 1024),
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-\n1",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got := TraceParent(input)
			if !traceParentPattern.MatchString(got) {
				t.Fatalf("generated traceparent = %q", got)
			}
			if got == strings.ToLower(strings.TrimSpace(input)) {
				t.Fatalf("invalid input was retained: %q", got)
			}
		})
	}
}

func TestTraceParentGenerationUsesFreshIdentifiers(t *testing.T) {
	first := TraceParent("")
	second := TraceParent("")
	if first == second {
		t.Fatalf("generated duplicate traceparents: %q", first)
	}
}
