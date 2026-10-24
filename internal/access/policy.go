package access

import (
	"context"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

type Action string

const (
	ActionViewOverview     Action = "view_overview"
	ActionViewSchema       Action = "view_schema"
	ActionViewData         Action = "view_data"
	ActionViewHistory      Action = "view_history"
	ActionManageDrafts     Action = "manage_drafts"
	ActionExecuteMigration Action = "execute_migration"
	ActionExplainMigration Action = "explain_migration"
)

func MinimumLevel(a Action) (domain.AccessLevel, bool) {
	switch a {
	case ActionViewOverview, ActionViewSchema, ActionViewData, ActionViewHistory:
		return domain.LevelView, true
	case ActionManageDrafts, ActionExecuteMigration, ActionExplainMigration:
		return domain.LevelEdit, true
	default:
		return domain.LevelNone, false
	}
}

func Can(level domain.AccessLevel, a Action) bool {
	min, ok := MinimumLevel(a)
	return ok && level >= min
}

type LevelStore interface {
	EffectiveLevel(ctx context.Context, userID, dbID uuid.UUID) (domain.AccessLevel, error)
}

type Policy struct {
	store LevelStore
}

func New(store LevelStore) *Policy {
	return &Policy{store: store}
}

func (p *Policy) Level(ctx context.Context, user domain.User, dbID uuid.UUID) (domain.AccessLevel, error) {
	if user.IsPlatformAdmin {
		return domain.LevelNone, nil
	}
	return p.store.EffectiveLevel(ctx, user.ID, dbID)
}
