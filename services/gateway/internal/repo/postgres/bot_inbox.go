package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrBotEventConflict = errors.New("bot event payload does not match")

type BotDelivery struct {
	Reply     json.RawMessage `json:"reply"`
	Delivered bool            `json:"delivered"`
}

func (q *Queries) ReadBotDelivery(ctx context.Context, eventID string, hash [32]byte) (*BotDelivery, error) {
	var storedHash, raw []byte
	var state string
	err := q.db.QueryRow(ctx, `SELECT payload_hash,state,result_ref FROM gateway_ops.inbox
WHERE producer='max_bot' AND event_id=$1`, eventID).Scan(&storedHash, &state, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapQueryError("read bot delivery", err)
	}
	if !bytes.Equal(hash[:], storedHash) {
		return nil, ErrBotEventConflict
	}
	if state != "processed" {
		return nil, nil
	}
	var delivery BotDelivery
	if json.Unmarshal(raw, &delivery) != nil || len(delivery.Reply) == 0 || !json.Valid(delivery.Reply) {
		return nil, errors.New("stored bot delivery is invalid")
	}
	return &delivery, nil
}

func (q *Queries) PrepareBotReply(ctx context.Context, eventID string, hash [32]byte, reply json.RawMessage) (BotDelivery, error) {
	return q.PrepareBotReplyWith(ctx, eventID, hash, func() (json.RawMessage, error) { return reply, nil })
}

func (q *Queries) PrepareBotReplyWith(ctx context.Context, eventID string, hash [32]byte, prepare func() (json.RawMessage, error)) (BotDelivery, error) {
	if eventID == "" || len(eventID) > 512 || hash == ([32]byte{}) || prepare == nil {
		return BotDelivery{}, errors.New("invalid bot inbox input")
	}
	_, err := q.db.Exec(ctx, `INSERT INTO gateway_ops.inbox (producer,event_id,payload_hash,state,received_at)
VALUES ('max_bot',$1,$2,'received',clock_timestamp()) ON CONFLICT (producer,event_id) DO NOTHING`, eventID, hash[:])
	if err != nil {
		return BotDelivery{}, mapQueryError("insert bot inbox", err)
	}
	var storedHash, raw []byte
	var state string
	err = q.db.QueryRow(ctx, `SELECT payload_hash,state,result_ref FROM gateway_ops.inbox
WHERE producer='max_bot' AND event_id=$1 FOR UPDATE`, eventID).Scan(&storedHash, &state, &raw)
	if err != nil {
		return BotDelivery{}, mapQueryError("read bot inbox", err)
	}
	if !bytes.Equal(hash[:], storedHash) {
		return BotDelivery{}, ErrBotEventConflict
	}
	if state == "processed" {
		var delivery BotDelivery
		if json.Unmarshal(raw, &delivery) != nil || len(delivery.Reply) == 0 || !json.Valid(delivery.Reply) {
			return BotDelivery{}, errors.New("stored bot delivery is invalid")
		}
		return delivery, nil
	}
	reply, err := prepare()
	if err != nil {
		return BotDelivery{}, err
	}
	if len(reply) == 0 || len(reply) > 64*1024 || !json.Valid(reply) {
		return BotDelivery{}, errors.New("invalid bot reply")
	}
	delivery := BotDelivery{Reply: reply}
	encoded, err := json.Marshal(delivery)
	if err != nil {
		return BotDelivery{}, err
	}
	_, err = q.db.Exec(ctx, `UPDATE gateway_ops.inbox SET state='processed',result_ref=$2,processed_at=clock_timestamp()
WHERE producer='max_bot' AND event_id=$1`, eventID, encoded)
	if err != nil {
		return BotDelivery{}, mapQueryError("prepare bot reply", err)
	}
	return delivery, nil
}

func (q *Queries) MarkBotReplyDelivered(ctx context.Context, eventID string, hash [32]byte) error {
	result, err := q.db.Exec(ctx, `UPDATE gateway_ops.inbox SET result_ref=jsonb_set(result_ref,'{delivered}','true'::jsonb)
WHERE producer='max_bot' AND event_id=$1 AND payload_hash=$2 AND state='processed'`, eventID, hash[:])
	if err != nil {
		return mapQueryError("mark bot delivery", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("bot delivery is missing")
	}
	return nil
}
