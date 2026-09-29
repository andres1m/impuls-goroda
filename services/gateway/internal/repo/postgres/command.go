package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const findCommandResultSQL = `
SELECT request_hash, route_id, resulting_revision, http_status, response_body, created_at
FROM gateway_ops.command_result
WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3`

const insertCommandResultSQL = `
INSERT INTO gateway_ops.command_result (
    actor_id, operation, idempotency_key, request_hash, route_id,
    resulting_revision, http_status, response_body, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

const lockOwnedRouteSQL = `
SELECT id, owner_id, lifecycle_state, current_revision
FROM planning.route
WHERE id = $1 AND owner_id = $2
FOR UPDATE`

type CommandExecutor struct {
	transactor *Transactor
	clock      func() time.Time
}

func NewCommandExecutor(transactor *Transactor) (*CommandExecutor, error) {
	if transactor == nil {
		return nil, errors.New("transaction manager is required")
	}
	return &CommandExecutor{transactor: transactor, clock: time.Now}, nil
}

const (
	uuidByteLen = 16
	int16Bits   = 16
	lockKeySize = 4
)

func (e *CommandExecutor) Execute(
	ctx context.Context,
	envelope *command.Envelope,
	mutate func(*Queries) (command.Result, error),
) (command.Result, error) {
	if envelope == nil {
		return command.Result{}, errors.New("command envelope is required")
	}
	if err := envelope.Validate(); err != nil {
		return command.Result{}, err
	}
	if mutate == nil {
		return command.Result{}, errors.New("mutation callback is required")
	}
	var result command.Result
	txOpts := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	err := e.transactor.WithinTx(ctx, &txOpts, func(queries *Queries) error {
		var txErr error
		result, txErr = e.executeInTx(ctx, queries, envelope, mutate)
		return txErr
	})
	if err != nil {
		return command.Result{}, err
	}
	return result, nil
}

func (e *CommandExecutor) executeInTx(
	ctx context.Context,
	queries *Queries,
	envelope *command.Envelope,
	mutate func(*Queries) (command.Result, error),
) (command.Result, error) {
	if err := queries.lockCommandKey(ctx, envelope); err != nil {
		return command.Result{}, err
	}
	stored, storedHash, err := queries.findCommandResult(ctx, envelope)
	if err == nil {
		if storedHash != envelope.RequestHash {
			return command.Result{}, command.ErrIdempotencyKeyReused
		}
		stored.Replayed = true
		return stored, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return command.Result{}, err
	}
	result, err := mutate(queries)
	if err != nil {
		return command.Result{}, err
	}
	if err := result.Validate(); err != nil {
		return command.Result{}, fmt.Errorf("validate command result: %w", err)
	}
	result.CreatedAt = e.clock().UTC()
	if err := queries.insertCommandResult(ctx, envelope, &result); err != nil {
		return command.Result{}, err
	}
	return result, nil
}

func (q *Queries) lockCommandKey(ctx context.Context, envelope *command.Envelope) error {
	material := make([]byte, 0, uuidByteLen+len(envelope.Operation)+uuidByteLen)
	material = append(material, envelope.ActorID[:]...)
	material = append(material, []byte(envelope.Operation)...)
	material = append(material, envelope.Key[:]...)
	digest := sha256.Sum256(material)
	first := beInt32(digest[:lockKeySize])
	second := beInt32(digest[lockKeySize : lockKeySize*2])
	if _, err := q.db.Exec(ctx, `SELECT pg_advisory_xact_lock($1::integer, $2::integer)`, first, second); err != nil {
		return fmt.Errorf("lock command key: %w", err)
	}
	return nil
}

func beInt32(b []byte) int32 {
	hi := int32(binary.BigEndian.Uint16(b[:2]))
	lo := int32(binary.BigEndian.Uint16(b[2:lockKeySize]))
	return (hi << int16Bits) | lo
}

func (q *Queries) findCommandResult(ctx context.Context, envelope *command.Envelope) (command.Result, [32]byte, error) {
	var result command.Result
	var hash []byte
	var routeID pgtype.UUID
	var revision pgtype.Int8
	err := q.db.QueryRow(ctx, findCommandResultSQL,
		encodeUUID([16]byte(envelope.ActorID)), envelope.Operation, formatUUID(envelope.Key),
	).Scan(&hash, &routeID, &revision, &result.HTTPStatus, &result.ResponseBody, &result.CreatedAt)
	if err != nil {
		return command.Result{}, [32]byte{}, mapQueryError("find command result", err)
	}
	requestHash, err := decodeHash(hash)
	if err != nil {
		return command.Result{}, [32]byte{}, fmt.Errorf("decode command hash: %w", err)
	}
	if routeID.Valid {
		decoded, err := decodeUUID(routeID)
		if err != nil {
			return command.Result{}, [32]byte{}, fmt.Errorf("decode command route: %w", err)
		}
		id := domain.RouteID(decoded)
		result.RouteID = &id
	}
	if revision.Valid {
		number := domain.RouteRevisionNumber(revision.Int64)
		result.ResultingRevision = &number
	}
	if err := result.Validate(); err != nil {
		return command.Result{}, [32]byte{}, fmt.Errorf("invalid stored command result: %w", err)
	}
	return result, requestHash, nil
}

func (q *Queries) insertCommandResult(
	ctx context.Context,
	envelope *command.Envelope,
	result *command.Result,
) error {
	var routeID any
	var revision any
	if result.RouteID != nil {
		routeID = encodeUUID([16]byte(*result.RouteID))
	}
	if result.ResultingRevision != nil {
		revision = int64(*result.ResultingRevision)
	}
	_, err := q.db.Exec(ctx, insertCommandResultSQL,
		encodeUUID([16]byte(envelope.ActorID)), envelope.Operation, formatUUID(envelope.Key),
		envelope.RequestHash[:], routeID, revision, result.HTTPStatus, []byte(result.ResponseBody), result.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert command result: %w", err)
	}
	return nil
}

func (q *Queries) LockOwnedRoute(
	ctx context.Context,
	routeID domain.RouteID,
	actorID domain.UserID,
) (RouteAccess, error) {
	if routeID == (domain.RouteID{}) || actorID == (domain.UserID{}) {
		return RouteAccess{}, errors.New("route and actor identifiers are required")
	}
	return q.queryRouteAccess(ctx, lockOwnedRouteSQL, "lock owned route", routeID, actorID)
}

func (q *Queries) LockOwnedRoutes(
	ctx context.Context,
	routeIDs []domain.RouteID,
	actorID domain.UserID,
) ([]RouteAccess, error) {
	ordered := append([]domain.RouteID(nil), routeIDs...)
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i][:], ordered[j][:]) < 0 })
	accesses := make([]RouteAccess, 0, len(ordered))
	for i, routeID := range ordered {
		if i > 0 && routeID == ordered[i-1] {
			continue
		}
		access, err := q.LockOwnedRoute(ctx, routeID, actorID)
		if err != nil {
			return nil, err
		}
		accesses = append(accesses, access)
	}
	return accesses, nil
}

func RequireRevision(access RouteAccess, expected domain.RouteRevisionNumber) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if access.Revision != expected {
		return &command.RevisionConflictError{Current: access.Revision}
	}
	return nil
}

func formatUUID(id [16]byte) string {
	const hex = "0123456789abcdef"
	var formatted [36]byte
	index := 0
	for i, value := range id {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			formatted[index] = '-'
			index++
		}
		formatted[index] = hex[value>>4]
		formatted[index+1] = hex[value&15]
		index += 2
	}
	return string(formatted[:])
}
