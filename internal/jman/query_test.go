package jman

import (
	"encoding/json"
	"testing"
)

func TestHoverContentForms(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{`{"kind":"markdown","value":"**signature**"}`, "**signature**"},
		{`[{"language":"java","value":"String name()"},"documentation"]`, "String name()\n\ndocumentation"},
		{`"plain documentation"`, "plain documentation"},
		{`null`, ""},
	} {
		if got := hoverText(json.RawMessage(test.raw)); got != test.want {
			t.Fatalf("%s: got %q, want %q", test.raw, got, test.want)
		}
	}
}
