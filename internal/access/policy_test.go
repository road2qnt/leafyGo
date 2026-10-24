package access

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

type stubStore struct {
	level domain.AccessLevel
	err   error
	calls int
}

func (s *stubStore) EffectiveLevel(_ context.Context, _, _ uuid.UUID) (domain.AccessLevel, error) {
	s.calls++
	return s.level, s.err
}

func TestMinimumLevel(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   domain.AccessLevel
		ok     bool
	}{
		{"view overview", ActionViewOverview, domain.LevelView, true},
		{"view schema", ActionViewSchema, domain.LevelView, true},
		{"view data", ActionViewData, domain.LevelView, true},
		{"view history", ActionViewHistory, domain.LevelView, true},
		{"manage drafts", ActionManageDrafts, domain.LevelEdit, true},
		{"execute migration", ActionExecuteMigration, domain.LevelEdit, true},
		{"explain migration", ActionExplainMigration, domain.LevelEdit, true},
		{"unknown action", Action("delete_database"), domain.LevelNone, false},
		{"empty action", Action(""), domain.LevelNone, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MinimumLevel(tt.action)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("MinimumLevel(%q) = (%v, %v); want (%v, %v)", tt.action, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCanAllActionsAndLevels(t *testing.T) {
	tests := []struct {
		action     Action
		none, view bool
		edit       bool
	}{
		{ActionViewOverview, false, true, true},
		{ActionViewSchema, false, true, true},
		{ActionViewData, false, true, true},
		{ActionViewHistory, false, true, true},
		{ActionManageDrafts, false, false, true},
		{ActionExecuteMigration, false, false, true},
		{ActionExplainMigration, false, false, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			if got := Can(domain.LevelNone, tt.action); got != tt.none {
				t.Errorf("Can(none, %q) = %v; want %v", tt.action, got, tt.none)
			}
			if got := Can(domain.LevelView, tt.action); got != tt.view {
				t.Errorf("Can(view, %q) = %v; want %v", tt.action, got, tt.view)
			}
			if got := Can(domain.LevelEdit, tt.action); got != tt.edit {
				t.Errorf("Can(edit, %q) = %v; want %v", tt.action, got, tt.edit)
			}
		})
	}
}

func TestCanUnknownActionAlwaysFalse(t *testing.T) {
	unknown := []Action{Action(""), Action("unknown"), Action("admin")}
	levels := []domain.AccessLevel{domain.LevelNone, domain.LevelView, domain.LevelEdit}
	for _, a := range unknown {
		for _, level := range levels {
			if Can(level, a) {
				t.Fatalf("Can(%v, %q) = true; want false", level, a)
			}
		}
	}
}

func TestPolicyAdminAlwaysNone(t *testing.T) {
	st := &stubStore{level: domain.LevelEdit}
	p := New(st)
	got, err := p.Level(context.Background(), domain.User{ID: uuid.New(), IsPlatformAdmin: true}, uuid.New())
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if got != domain.LevelNone {
		t.Fatalf("Level = %v; want none", got)
	}
	if st.calls != 0 {
		t.Fatalf("store calls = %d; want 0", st.calls)
	}
}

func TestPolicyNonAdminDelegatesStore(t *testing.T) {
	st := &stubStore{level: domain.LevelEdit}
	p := New(st)
	got, err := p.Level(context.Background(), domain.User{ID: uuid.New()}, uuid.New())
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if got != domain.LevelEdit {
		t.Fatalf("Level = %v; want edit", got)
	}
	if st.calls != 1 {
		t.Fatalf("store calls = %d; want 1", st.calls)
	}
}

func TestAccessLevelOrdering(t *testing.T) {
	if !(domain.LevelEdit > domain.LevelView && domain.LevelView > domain.LevelNone) {
		t.Fatal("expected edit > view > none")
	}
	if !domain.LevelView.CanView() || domain.LevelNone.CanView() {
		t.Fatal("CanView wrong")
	}
	if !domain.LevelEdit.CanEdit() || domain.LevelView.CanEdit() {
		t.Fatal("CanEdit wrong")
	}
	if domain.ParseLevel("edit") != domain.LevelEdit || domain.ParseLevel("view") != domain.LevelView {
		t.Fatal("ParseLevel wrong")
	}
	if domain.ParseLevel("admin") != domain.LevelNone {
		t.Fatal("unknown level must map to none (admin flag is not DB access)")
	}
}
