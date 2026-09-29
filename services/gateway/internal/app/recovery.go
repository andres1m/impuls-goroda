package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

var ErrInvalidRoutePage = errors.New("invalid route page")

type RoutePageRequest struct {
	Lifecycle string
	Limit     int
	Cursor    string
}

type routeCursor struct {
	Version   int       `json:"v"`
	Lifecycle string    `json:"lifecycle"`
	UpdatedAt time.Time `json:"updated_at"`
	RouteID   string    `json:"route_id"`
}

func (r *Runtime) ListRoutes(ctx context.Context, actor d.UserID, request RoutePageRequest) (routewire.RoutePage, error) {
	if r.queries == nil {
		return routewire.RoutePage{}, errors.New("gateway queries are not initialized")
	}
	if request.Limit < 1 || request.Limit > 100 || (request.Lifecycle != "" && request.Lifecycle != "draft" && request.Lifecycle != "saved") {
		return routewire.RoutePage{}, ErrInvalidRoutePage
	}
	query := postgres.RoutePageQuery{Lifecycle: request.Lifecycle, Limit: request.Limit + 1}
	if request.Cursor != "" {
		cursor, id, err := decodeRouteCursor(request.Cursor, request.Lifecycle)
		if err != nil {
			return routewire.RoutePage{}, err
		}
		query.BeforeTime, query.BeforeID = &cursor.UpdatedAt, d.RouteID(id)
	}
	routes, err := r.queries.ListOwnerRoutes(ctx, actor, query)
	if err != nil {
		return routewire.RoutePage{}, err
	}
	page := routewire.RoutePage{Routes: routes}
	if len(routes) > request.Limit {
		page.Routes = routes[:request.Limit]
		last := page.Routes[len(page.Routes)-1]
		raw, err := json.Marshal(routeCursor{Version: 1, Lifecycle: request.Lifecycle, UpdatedAt: last.UpdatedAt.UTC(), RouteID: last.RouteID})
		if err != nil {
			return routewire.RoutePage{}, err
		}
		encoded := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &encoded
	}
	return page, nil
}

func decodeRouteCursor(encoded, lifecycle string) (routeCursor, uuid.UUID, error) {
	if len(encoded) > 512 {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	var cursor routeCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	id, err := uuid.Parse(cursor.RouteID)
	if err != nil || id == uuid.Nil || id.String() != cursor.RouteID || cursor.Version != 1 || cursor.Lifecycle != lifecycle || cursor.UpdatedAt.IsZero() || cursor.UpdatedAt.Year() < 1 || cursor.UpdatedAt.Year() > 9999 {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, raw) {
		return routeCursor{}, uuid.Nil, ErrInvalidRoutePage
	}
	return cursor, id, nil
}
