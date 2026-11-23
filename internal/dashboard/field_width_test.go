package dashboard

import "testing"

func TestClipField(t *testing.T) {
	for _, c := range []struct {
		input string
		width int
		want  string
	}{
		{"abcdef", 5, "ab..."}, {"東京", 1, ""}, {"東京", 3, "東"},
		{"e\u0301x", 1, "e\u0301"}, {"👩‍💻x", 2, "👩‍💻"}, {"/東京/é/🙂", 10, "/東京/é/🙂"},
	} {
		if got := clipField(c.input, c.width, true); got != c.want {
			t.Errorf("%q at %d: got %q, want %q", c.input, c.width, got, c.want)
		}
	}
}
