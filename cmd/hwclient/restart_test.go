package main

import (
	"slices"
	"testing"
)

func TestRelaunchArgsAddsMissingFlagsOnce(t *testing.T) {
	current := []string{"-data", "x"}
	got := relaunchArgs(current, []string{"-no-browser"})
	if want := []string{"-data", "x", "-no-browser"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if again := relaunchArgs(got, []string{"-no-browser"}); !slices.Equal(again, got) {
		t.Errorf("flag added twice: %q", again)
	}
	if len(current) != 2 {
		t.Error("the input slice was changed")
	}
}
