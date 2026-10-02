package filter

import "testing"

func TestMask(t *testing.T) {
	f := New([]string{"heck"})
	cases := map[string]string{
		"well shit":            "well s***",
		"Fucking hell":         "F****** hell",
		"you absolute Bastard": "you absolute B******",
		"class assignment":     "class assignment", // no substring matches
		"what the heck!":       "what the h***!",
		"clean sentence":       "clean sentence",
	}
	for in, want := range cases {
		if got := f.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
	var nilf *Filter
	if nilf.Mask("shit") != "shit" {
		t.Error("nil filter must be a no-op")
	}
}
