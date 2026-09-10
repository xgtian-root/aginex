package envfile

import (
	"strings"
	"testing"
)

func TestLiteralRoundTripPreservesOtherLines(t *testing.T) {
	source := []byte("# heading\nKEEP='a # value'\nexport CHANGE=old # note\nREMOVE=yes\n")
	value := `p@ss$word # 'quoted' "x"`
	out, err := Edit(source, map[string]*string{"CHANGE": &value, "REMOVE": nil})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if parsed["CHANGE"] != value || parsed["KEEP"] != "a # value" {
		t.Fatalf("incorrect parse: %#v", parsed)
	}
	for _, text := range []string{"# heading", "KEEP='a # value'", "# note"} {
		if !strings.Contains(string(out), text) {
			t.Errorf("lost %s", text)
		}
	}
	if _, ok := parsed["REMOVE"]; ok {
		t.Fatal("unset failed")
	}
}
func TestRejectAmbiguousDotenv(t *testing.T) {
	for _, v := range []string{"A=x\nA=y", "no assignment", "A='unclosed", "A=\"x\" invalid"} {
		if _, e := Parse([]byte(v)); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
}
