package maxbot

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var ErrInvalidDeliveryUpdate = errors.New("invalid MAX delivery update")

type DeliveryUpdate struct {
	Type       string
	Timestamp  int64
	ChatID     int64
	UserID     int64
	MutedUntil *int64
}

func DecodeDeliveryUpdate(raw json.RawMessage) (*DeliveryUpdate, error) {
	if len(raw) == 0 || len(raw) > 64*1024 || !utf8.Valid(raw) {
		return nil, ErrInvalidDeliveryUpdate
	}
	var input struct {
		Type       string `json:"update_type"`
		Timestamp  *int64 `json:"timestamp"`
		ChatID     *int64 `json:"chat_id"`
		MutedUntil *int64 `json:"muted_until"`
		User       *struct {
			ID int64 `json:"user_id"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Type == "" {
		return nil, ErrInvalidDeliveryUpdate
	}
	switch input.Type {
	case "bot_started", "bot_stopped", "dialog_removed", "dialog_muted", "dialog_unmuted":
	default:
		return nil, nil
	}
	if input.Timestamp == nil || *input.Timestamp <= 0 || input.ChatID == nil || *input.ChatID == 0 || input.User == nil || input.User.ID <= 0 {
		return nil, ErrInvalidDeliveryUpdate
	}
	if input.Type == "dialog_muted" && input.MutedUntil == nil {
		return nil, ErrInvalidDeliveryUpdate
	}
	update := &DeliveryUpdate{Type: input.Type, Timestamp: *input.Timestamp, ChatID: *input.ChatID, UserID: input.User.ID}
	if input.Type == "dialog_muted" {
		update.MutedUntil = input.MutedUntil
	}
	return update, nil
}
