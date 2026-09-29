package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

func (h *BotHandler) HandleRaw(ctx context.Context, raw json.RawMessage) error {
	if h.runtime == nil || h.runtime.transactor == nil {
		return errors.New("bot runtime is not initialized")
	}
	if h.runtime.cfg.MAXDeliveryEventsEnabled {
		update, err := maxbot.DecodeDeliveryUpdate(raw)
		if err != nil {
			return err
		}
		if update != nil {
			axis, restricted := postgres.MAXDeliveryStopped, true
			switch update.Type {
			case "bot_started":
				restricted = false
			case "dialog_muted":
				axis = postgres.MAXDeliveryMuted
			case "dialog_unmuted":
				axis, restricted = postgres.MAXDeliveryMuted, false
			}
			key, err := json.Marshal(struct {
				Type                      string
				Timestamp, UserID, ChatID int64
			}{update.Type, update.Timestamp, update.UserID, update.ChatID})
			if err != nil {
				return err
			}
			digest := sha256.Sum256(key)
			canonical, err := json.Marshal(update)
			if err != nil {
				return err
			}
			event := postgres.MAXDeliveryEvent{ID: hex.EncodeToString(digest[:]), Hash: sha256.Sum256(canonical),
				MAXUserID: update.UserID, Axis: axis, Restricted: restricted, TimestampMS: update.Timestamp}
			workCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = h.runtime.transactor.WithinTx(workCtx, nil, func(q *postgres.Queries) error {
				return q.ReceiveMAXDeliveryEvent(workCtx, event, h.runtime.clock().UTC())
			})
			cancel()
			if err != nil {
				if errors.Is(err, postgres.ErrBotEventConflict) {
					return err
				}
				return errors.New("MAX delivery state unavailable")
			}
			if update.Type != "bot_started" {
				return nil
			}
		}
	}
	var update maxbot.Update
	if err := json.Unmarshal(raw, &update); err != nil {
		return maxbot.ErrInvalidDeliveryUpdate
	}
	return h.Handle(ctx, &update)
}
