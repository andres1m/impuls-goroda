package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Landing struct {
	pool *pgxpool.Pool
}

func NewLanding(pool *pgxpool.Pool) *Landing {
	return &Landing{pool: pool}
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (l *Landing) EnsureSource(ctx context.Context, source domain.Source) (ingest.SourceID, error) {
	return upsertSource(ctx, l.pool, source)
}

func upsertSource(ctx context.Context, q rowQuerier, source domain.Source) (ingest.SourceID, error) {
	var id pgtype.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO integration.source
			(id, source_key, name, documentation_url, access_mode, license_info, schema_version, is_enabled)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, true)
		ON CONFLICT (source_key) DO UPDATE SET
			name = EXCLUDED.name,
			documentation_url = EXCLUDED.documentation_url,
			access_mode = EXCLUDED.access_mode,
			license_info = EXCLUDED.license_info,
			schema_version = EXCLUDED.schema_version
		RETURNING id`,
		string(source.Key), source.Name, nullIfEmpty(source.DocumentationURL), string(source.AccessMode),
		nullIfEmpty(source.LicenseInfo), source.SchemaVersion,
	).Scan(&id)
	if err != nil {
		return ingest.SourceID{}, fmt.Errorf("upsert source: %w", err)
	}
	return ingest.SourceID(id.Bytes), nil
}

func (l *Landing) Cursor(ctx context.Context, source ingest.SourceID, city domain.City) (json.RawMessage, error) {
	var cursor []byte
	err := l.pool.QueryRow(ctx, `
		SELECT fetch_cursor FROM integration.sync_cursor WHERE source_id = $1 AND city = $2`,
		uuidParam(source), string(city),
	).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cursor: %w", err)
	}
	return cursor, nil
}

func (l *Landing) SaveRecord(ctx context.Context, source ingest.SourceID, city domain.City, mode domain.DataMode, record domain.RawRecord, fetchedAt time.Time) (bool, error) {
	hash := record.ContentHash
	if hash == nil {
		sum := sha256.Sum256(record.Payload)
		hash = sum[:]
	}
	inserted := false
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		var recordID pgtype.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO integration.source_record
				(id, source_id, city, external_id, source_url, last_seen_at, source_updated_at, provider_version, data_mode)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (source_id, external_id) DO UPDATE SET
				city = EXCLUDED.city,
				source_url = EXCLUDED.source_url,
				last_seen_at = EXCLUDED.last_seen_at,
				source_updated_at = EXCLUDED.source_updated_at,
				provider_version = EXCLUDED.provider_version,
				data_mode = EXCLUDED.data_mode
			RETURNING id`,
			uuidParam(source), string(city), record.ExternalID, record.SourceURL, fetchedAt,
			record.SourceUpdatedAt, nullIfEmpty(record.ProviderVersion), string(mode),
		).Scan(&recordID)
		if err != nil {
			return fmt.Errorf("upsert source record: %w", err)
		}

		var lastHash []byte
		err = tx.QueryRow(ctx, `
			SELECT content_hash FROM integration.raw_ingest
			WHERE source_record_id = $1
			ORDER BY fetched_at DESC
			LIMIT 1`, recordID,
		).Scan(&lastHash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read last hash: %w", err)
		}
		if bytes.Equal(lastHash, hash) {
			return nil
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO integration.raw_ingest
				(id, source_record_id, raw_payload, content_type, content_hash, fetched_at, source_updated_at, data_mode, processing_state)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, 'pending')`,
			recordID, record.Payload, record.ContentType, hash, fetchedAt, record.SourceUpdatedAt, string(mode),
		)
		if err != nil {
			return fmt.Errorf("insert raw ingest: %w", err)
		}
		inserted = true
		return nil
	})
	return inserted, err
}

func (l *Landing) FinishRun(ctx context.Context, run ingest.Run) error {
	if run.ErrorCode != "" {
		_, err := l.pool.Exec(ctx, `
			INSERT INTO integration.sync_cursor (source_id, city, last_attempt_at, last_error_code)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (source_id, city) DO UPDATE SET
				last_attempt_at = EXCLUDED.last_attempt_at,
				last_error_code = EXCLUDED.last_error_code`,
			uuidParam(run.SourceID), string(run.City), run.AttemptAt, run.ErrorCode,
		)
		return err
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO integration.sync_cursor (source_id, city, fetch_cursor, last_attempt_at, last_success_at, last_error_code)
		VALUES ($1, $2, $3, $4, $4, NULL)
		ON CONFLICT (source_id, city) DO UPDATE SET
			fetch_cursor = EXCLUDED.fetch_cursor,
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_success_at = EXCLUDED.last_success_at,
			last_error_code = NULL`,
		uuidParam(run.SourceID), string(run.City), cursorParam(run.Cursor), run.AttemptAt,
	)
	return err
}

func uuidParam(id ingest.SourceID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func cursorParam(cursor json.RawMessage) any {
	if len(cursor) == 0 {
		return nil
	}
	return string(cursor)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
