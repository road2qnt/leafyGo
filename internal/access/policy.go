package access

import (
	"context"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

type LevelStore interface {
	EffectiveLevel(ctx context.Context, userID, dbID uuid.UUID) (domain.AccessLevel, error)
}

// Policy resolves a user's effective access on a database. Access is granted
// only through groups; when several groups grant access, the most permissive
// level wins. Platform admin is deliberately not a database access level.
type Policy struct {
	store LevelStore
}

func New(store LevelStore) *Policy {
	return &Policy{store: store}
}

func (p *Policy) Level(ctx context.Context, user domain.User, dbID uuid.UUID) (domain.AccessLevel, error) {
	return p.store.EffectiveLevel(ctx, user.ID, dbID)
}
