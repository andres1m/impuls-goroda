package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func (q *Queries) LookupCommand(ctx context.Context, envelope command.Envelope) (command.Result, bool, error) {
	if err := envelope.Validate(); err != nil {
		return command.Result{}, false, err
	}
	result, hash, err := q.findCommandResult(ctx, &envelope)
	if errors.Is(err, ErrNotFound) {
		return command.Result{}, false, nil
	}
	if err != nil {
		return command.Result{}, false, err
	}
	if hash != envelope.RequestHash {
		return command.Result{}, false, command.ErrIdempotencyKeyReused
	}
	result.Replayed = true
	return result, true, nil
}

func (q *Queries) SaveDraft(ctx context.Context, access RouteAccess, now time.Time) (domain.RouteRevisionNumber, error) {
	if access.Lifecycle != domain.RouteDraft || access.Revision <= 0 || access.Revision == math.MaxInt64 || now.IsZero() {
		return 0, errors.New("invalid draft save")
	}
	revision, err := q.cloneRevisionWithLifecycle(ctx, access.RouteID, access.Revision, domain.VisitID{}, domain.MutationSave, nil, now, domain.RouteSaved)
	if err != nil {
		return 0, err
	}
	id := encodeUUID([16]byte(access.RouteID))
	_, err = q.db.Exec(ctx, `UPDATE planning.route SET lifecycle_state = 'saved', draft_expires_at = NULL
WHERE id = $1`, id)
	if err != nil {
		return 0, fmt.Errorf("save route: %w", err)
	}
	return revision, nil
}
