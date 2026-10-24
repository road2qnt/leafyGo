package access

import (
	"context"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

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
	return p.store.EffectiveLevel(ctx, user.ID, dbID)
}
