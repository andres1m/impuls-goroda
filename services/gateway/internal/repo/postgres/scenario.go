package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (q *Queries) CreateBotScenario(ctx context.Context, owner d.UserID, scenario routewire.BotScenario) error {
	if owner == (d.UserID{}) || scenario.Version != "1" || scenario.Status != "draft" || scenario.Outcome != nil {
		return errors.New("invalid new scenario")
	}
	scenario.UpdatedAt = scenario.UpdatedAt.UTC().Truncate(time.Microsecond)
	raw, err := routewire.EncodeBotScenarioState(scenario)
	if err != nil {
		return errors.New("invalid new scenario")
	}
	_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.conversation
(user_id,conversation_key,step,state_version,confirmed_input,pending_extraction,selected_route_id,updated_at,expires_at)
VALUES ($1,$2,'draft',1,'{}'::jsonb,$3,NULL,$4,NULL)`,
		encodeUUID([16]byte(owner)), scenarioKey(scenario.ScenarioID), raw, scenario.UpdatedAt)
	if err != nil {
		return mapQueryError("create bot scenario", err)
	}
	return nil
}

func (q *Queries) ReadBotScenario(ctx context.Context, owner d.UserID, scenarioID string) (routewire.BotScenario, error) {
	if owner == (d.UserID{}) || !routewire.ValidScenarioID(scenarioID) {
		return routewire.BotScenario{}, ErrNotFound
	}
	return scanBotScenario(q.db.QueryRow(ctx, `SELECT conversation_key,step,state_version,updated_at,pending_extraction,confirmed_input
FROM gateway_ops.conversation
WHERE user_id=$1 AND conversation_key=$2
AND (expires_at IS NULL OR expires_at>statement_timestamp())`, encodeUUID([16]byte(owner)), scenarioKey(scenarioID)))
}

func (q *Queries) ReadLatestBotScenario(ctx context.Context, owner d.UserID) (*routewire.BotScenario, error) {
	if owner == (d.UserID{}) {
		return nil, errors.New("scenario owner is required")
	}
	scenario, err := scanBotScenario(q.db.QueryRow(ctx, `SELECT conversation_key,step,state_version,updated_at,pending_extraction,confirmed_input
FROM gateway_ops.conversation
WHERE user_id=$1 AND conversation_key LIKE 'scenario:%'
AND (expires_at IS NULL OR expires_at>statement_timestamp())
ORDER BY updated_at DESC,conversation_key DESC LIMIT 1`, encodeUUID([16]byte(owner))))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scenario, nil
}

func scanBotScenario(row interface{ Scan(...any) error }) (routewire.BotScenario, error) {
	var key, step string
	var version int64
	var updated time.Time
	var raw, confirmed []byte
	if err := row.Scan(&key, &step, &version, &updated, &raw, &confirmed); err != nil {
		return routewire.BotScenario{}, mapQueryError("read bot scenario", err)
	}
	scenario, err := routewire.DecodeBotScenarioState(raw)
	if err != nil || key != scenarioKey(scenario.ScenarioID) || step != scenario.Status ||
		scenario.Version != strconv.FormatInt(version, 10) || !updated.Equal(scenario.UpdatedAt) {
		return routewire.BotScenario{}, errors.New("stored bot scenario is invalid")
	}
	if scenario.Outcome != nil {
		stored, err := routewire.DecodeInput(confirmed)
		if err != nil {
			return routewire.BotScenario{}, errors.New("stored confirmed scenario input is invalid")
		}
		inputRaw, err := json.Marshal(scenario.Input)
		if err != nil {
			return routewire.BotScenario{}, errors.New("stored bot scenario is invalid")
		}
		expected, err := routewire.DecodeInput(inputRaw)
		if err != nil {
			return routewire.BotScenario{}, errors.New("stored bot scenario is invalid")
		}
		storedRaw, err := json.Marshal(stored)
		if err != nil {
			return routewire.BotScenario{}, errors.New("stored confirmed scenario input is invalid")
		}
		expectedRaw, err := json.Marshal(expected)
		if err != nil || !bytes.Equal(storedRaw, expectedRaw) {
			return routewire.BotScenario{}, errors.New("stored confirmed scenario input disagrees")
		}
	} else if strings.TrimSpace(string(confirmed)) != "{}" {
		return routewire.BotScenario{}, errors.New("unconfirmed scenario contains confirmed input")
	}
	return scenario, nil
}

func scenarioKey(id string) string {
	return "scenario:" + strings.ToLower(id)
}

func RequireScenarioVersion(scenario routewire.BotScenario, expected int64) error {
	current, err := strconv.ParseInt(scenario.Version, 10, 64)
	if err != nil || expected < 1 {
		return errors.New("invalid scenario version")
	}
	if scenario.Status == "completed" {
		return command.ErrScenarioCompleted
	}
	if current != expected {
		return &command.ScenarioVersionConflict{Current: current}
	}
	return nil
}

func (q *Queries) LockBotScenario(ctx context.Context, owner d.UserID, scenarioID string, expected int64) (routewire.BotScenario, error) {
	if owner == (d.UserID{}) || !routewire.ValidScenarioID(scenarioID) {
		return routewire.BotScenario{}, ErrNotFound
	}
	scenario, err := scanBotScenario(q.db.QueryRow(ctx, `SELECT conversation_key,step,state_version,updated_at,pending_extraction,confirmed_input
FROM gateway_ops.conversation WHERE user_id=$1 AND conversation_key=$2
AND (expires_at IS NULL OR expires_at>statement_timestamp()) FOR UPDATE`, encodeUUID([16]byte(owner)), scenarioKey(scenarioID)))
	if err != nil {
		return routewire.BotScenario{}, err
	}
	if err := RequireScenarioVersion(scenario, expected); err != nil {
		return routewire.BotScenario{}, err
	}
	return scenario, nil
}

func (q *Queries) RecordScenarioOutcome(ctx context.Context, owner d.UserID, scenario routewire.BotScenario, input routewire.ConfirmedRouteInput, outcome routewire.BotScenarioOutcome, now time.Time) error {
	version, err := strconv.ParseInt(scenario.Version, 10, 64)
	if err != nil || version < 1 || version == math.MaxInt64 || owner == (d.UserID{}) || scenario.Status != "draft" {
		return errors.New("invalid scenario completion")
	}
	confirmed, err := json.Marshal(input)
	if err != nil {
		return err
	}
	partial, err := routewire.DecodeBotScenarioInput(confirmed)
	if err != nil {
		return err
	}
	scenario.Input, scenario.PendingExtraction, scenario.Outcome = partial, nil, &outcome
	scenario.Version = strconv.FormatInt(version+1, 10)
	scenario.UpdatedAt = now.UTC().Truncate(time.Microsecond)
	if outcome.Status == "READY" || outcome.Status == "PARTIAL" {
		scenario.Status = "completed"
	}
	raw, err := routewire.EncodeBotScenarioState(scenario)
	if err != nil {
		return err
	}
	result, err := q.db.Exec(ctx, `UPDATE gateway_ops.conversation
SET step=$4,state_version=state_version+1,confirmed_input=$5,pending_extraction=$6,updated_at=$7
WHERE user_id=$1 AND conversation_key=$2 AND state_version=$3 AND step='draft'
AND (expires_at IS NULL OR expires_at>statement_timestamp())`, encodeUUID([16]byte(owner)), scenarioKey(scenario.ScenarioID), version,
		scenario.Status, confirmed, raw, scenario.UpdatedAt)
	if err != nil {
		return mapQueryError("record scenario outcome", err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
