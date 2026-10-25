package domain

import "testing"

func TestParseLevel(t *testing.T) {
	tests := map[string]AccessLevel{
		"view": LevelView, "VIEW": LevelView, " edit ": LevelEdit, "Edit": LevelEdit,
		"": LevelNone, "none": LevelNone, "admin": LevelNone, "view,edit": LevelNone,
	}
	for in, want := range tests {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestAccessLevelPredicates(t *testing.T) {
	tests := []struct {
		level      AccessLevel
		str, lbl   string
		view, edit bool
	}{
		{LevelNone, "none", "No access", false, false},
		{LevelView, "view", "View Only", true, false},
		{LevelEdit, "edit", "View + Edit", true, true},
	}
	for _, tt := range tests {
		if tt.level.String() != tt.str || tt.level.Label() != tt.lbl {
			t.Errorf("level %d = %q / %q", tt.level, tt.level.String(), tt.level.Label())
		}
		if tt.level.CanView() != tt.view || tt.level.CanEdit() != tt.edit {
			t.Errorf("level %v CanView=%v CanEdit=%v", tt.level, tt.level.CanView(), tt.level.CanEdit())
		}
	}
}

func TestAccessLevelOrdering(t *testing.T) {
	if !(LevelNone < LevelView && LevelView < LevelEdit) {
		t.Fatal("levels must be ordered none < view < edit for highest-wins comparisons")
	}
}
