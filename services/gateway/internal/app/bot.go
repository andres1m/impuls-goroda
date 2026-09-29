package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BotHandler struct {
	runtime *Runtime
	client  *maxbot.Client
	locks   [64]sync.Mutex
}

func NewBotHandler(runtime *Runtime, client *maxbot.Client) *BotHandler {
	return &BotHandler{runtime: runtime, client: client}
}

func (h *BotHandler) Handle(ctx context.Context, update maxbot.Update) error {
	if h.runtime == nil || h.client == nil || h.runtime.transactor == nil || h.runtime.queries == nil {
		return errors.New("bot runtime is not initialized")
	}
	reply, scenario, handled, err := h.client.PrepareScenarioReply(update)
	if err != nil {
		return err
	}
	if !handled {
		return nil
	}
	encoded, err := json.Marshal(update)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(encoded)
	lockHash := sha256.Sum256([]byte(reply.EventID))
	lock := &h.locks[int(lockHash[0])%len(h.locks)]
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var delivery postgres.BotDelivery
	err = h.runtime.transactor.WithinTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(q *postgres.Queries) error {
		var err error
		delivery, err = q.PrepareBotReplyWith(ctx, reply.EventID, hash, func() (json.RawMessage, error) {
			if scenario == nil {
				return reply.Body, nil
			}
			return h.createScenarioReply(ctx, q, reply.UserID, *scenario)
		})
		return err
	})
	if err != nil || delivery.Delivered {
		return err
	}
	if err := h.client.SendReply(ctx, reply.UserID, delivery.Reply); err != nil {
		return err
	}
	return h.runtime.queries.MarkBotReplyDelivered(ctx, reply.EventID, hash)
}

func (h *BotHandler) createScenarioReply(ctx context.Context, q *postgres.Queries, maxUserID int64, request maxbot.ScenarioRequest) (json.RawMessage, error) {
	accountID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := h.runtime.clock().UTC()
	account, err := q.UpsertMaxAccount(ctx, d.UserAccount{
		ID: d.UserID(accountID), MaxUserID: strconv.FormatInt(maxUserID, 10),
		State: d.AccountActive, Kind: d.AccountMax, CreatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		return nil, err
	}
	if account.State != d.AccountActive {
		return nil, postgres.ErrAccountDisabled
	}
	if request.Resume {
		latest, err := q.ReadLatestBotScenario(ctx, account.ID)
		if err != nil {
			return nil, err
		}
		if latest != nil {
			return h.client.ResumeScenarioReply(latest.ScenarioID, latest.Status == "completed")
		}
		return h.client.ScenarioMenuReply()
	}
	scenarioID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	scenario := routewire.BotScenario{
		ScenarioID: scenarioID.String(), Version: "1", Source: "custom",
		Status: "draft", UpdatedAt: now,
	}
	if request.PresetID != "" {
		scenario.Source, scenario.PresetID = "preset", &request.PresetID
	} else {
		scenario.SourceText = &request.SourceText
	}
	if err := q.CreateBotScenario(ctx, account.ID, scenario); err != nil {
		return nil, err
	}
	return h.client.ScenarioReply(scenario.ScenarioID)
}
