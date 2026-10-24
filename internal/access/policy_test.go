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
}

func (s stubStore) EffectiveLevel(_ context.Context, _, _ uuid.UUID) (domain.AccessLevel, error) {
	return s.level, s.err
}

func TestPolicyPassesThroughEffectiveLevel(t *testing.T) {
	p := New(stubStore{level: domain.LevelEdit})
	got, err := p.Level(context.Background(), domain.User{ID: uuid.New()}, uuid.New())
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if got != domain.LevelEdit {
		t.Fatalf("Level = %v; want edit", got)
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
