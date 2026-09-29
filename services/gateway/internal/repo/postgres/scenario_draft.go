package postgres

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (q *Queries) SaveBotScenarioDraft(ctx context.Context, owner d.UserID, scenario routewire.BotScenario, input routewire.BotScenarioInput, now time.Time) (routewire.BotScenario, error) {
	version, err := strconv.ParseInt(scenario.Version, 10, 64)
	if err != nil || version < 1 || version == math.MaxInt64 || owner == (d.UserID{}) || scenario.Status != "draft" || now.IsZero() {
		return routewire.BotScenario{}, errors.New("invalid scenario draft")
	}
	scenario.Input, scenario.Outcome = input, nil
	scenario.Version = strconv.FormatInt(version+1, 10)
	scenario.UpdatedAt = now.UTC().Truncate(time.Microsecond)
	raw, err := routewire.EncodeBotScenarioState(scenario)
	if err != nil {
		return routewire.BotScenario{}, err
	}
	result, err := q.db.Exec(ctx, `UPDATE gateway_ops.conversation
SET state_version=state_version+1,confirmed_input='{}'::jsonb,pending_extraction=$4,updated_at=$5
WHERE user_id=$1 AND conversation_key=$2 AND state_version=$3 AND step='draft'
AND (expires_at IS NULL OR expires_at>statement_timestamp())`, encodeUUID([16]byte(owner)), scenarioKey(scenario.ScenarioID), version, raw, scenario.UpdatedAt)
	if err != nil {
		return routewire.BotScenario{}, mapQueryError("save scenario draft", err)
	}
	if result.RowsAffected() != 1 {
		return routewire.BotScenario{}, ErrNotFound
	}
	return scenario, nil
}
