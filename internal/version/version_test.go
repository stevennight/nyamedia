package version

import "testing"

func TestIsVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-version"}, {"version"}} {
		if !IsVersionCommand(args) {
			t.Fatalf("IsVersionCommand(%q) = false", args)
		}
	}

	for _, args := range [][]string{nil, {}, {"--version", "extra"}, {"-v"}} {
		if IsVersionCommand(args) {
			t.Fatalf("IsVersionCommand(%q) = true", args)
		}
	}
}
