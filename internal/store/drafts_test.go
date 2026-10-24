package store

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestHasRevisionConflict(t *testing.T) {
	if hasRevisionConflict(3, 3) {
		t.Fatal("same revision must not conflict")
	}
	if !hasRevisionConflict(3, 2) {
		t.Fatal("different revision must conflict")
	}
	if !hasRevisionConflict(3, 0) || !hasRevisionConflict(3, -1) {
		t.Fatal("non-positive token must conflict")
	}
}

func TestApplyDraftUpdate(t *testing.T) {
	actor := uuid.New()
	now := time.Now().UTC()
	original := domain.MigrationDraft{
		ID:        uuid.New(),
		DBID:      uuid.New(),
		Title:     "old",
		SQLText:   "select 1",
		Revision:  4,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}

	got := applyDraftUpdate(original, "new", "select 2", actor, now)
	if got.Revision != original.Revision+1 {
		t.Fatalf("revision = %d; want %d", got.Revision, original.Revision+1)
	}
	if got.Title != "new" || got.SQLText != "select 2" {
		t.Fatalf("title/sql = %q/%q", got.Title, got.SQLText)
	}
	if got.UpdatedBy == nil || *got.UpdatedBy != actor {
		t.Fatalf("UpdatedBy = %v; want %s", got.UpdatedBy, actor)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v; want %v", got.UpdatedAt, now)
	}
	if got.ID != original.ID || got.DBID != original.DBID || !got.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal("immutable fields changed")
	}
}
