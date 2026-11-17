package desktop

import (
	"strings"
	"testing"
)

func TestWindowTitle(t *testing.T) {
	for in, want := range map[string]string{
		"JSON Format/Validate — devknife": "JSON Format/Validate — devknife",
		"  a\n\tb  ":                      "a b",
		"":                                "devknife",
		" \n ":                            "devknife",
	} {
		if got := windowTitle(in); got != want {
			t.Errorf("windowTitle(%q) = %q, want %q", in, got, want)
		}
	}
	long := windowTitle(strings.Repeat("é", 500))
	if n := len([]rune(long)); n != 120 {
		t.Errorf("long title has %d runes, want 120", n)
	}
}
