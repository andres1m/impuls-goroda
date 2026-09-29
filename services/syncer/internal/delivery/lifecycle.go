package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"

	gatewayv1 "github.com/andres1m/impuls-goroda/proto/gateway/v1"
)

const LifecycleTopic = "events.lifecycle.urgent"
const LifecycleEventType = "catalog.lifecycle.v1"
const lifecycleSendTimeout = 10 * time.Second
const lifecyclePartitions = 3

type EventSender map[string]Sender

func (s EventSender) Send(ctx context.Context, item *Item) error {
	if item == nil {
		return errors.New("outbox item is required")
	}
	sender, ok := s[item.EventType]
	if !ok || sender == nil {
		return fmt.Errorf("no sender for kafka event type %q", item.EventType)
	}
	return sender.Send(ctx, item)
}

func decodeLifecycle(item *Item) (*gatewayv1.DeliverCatalogLifecycleRequest, error) {
	if item == nil || item.EventType != LifecycleEventType {
		return nil, errors.New("invalid lifecycle delivery")
	}
	var request gatewayv1.DeliverCatalogLifecycleRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(item.Payload, &request); err != nil {
		return nil, fmt.Errorf("decode lifecycle delivery: %w", err)
	}
	if !bytes.Equal(request.DeliveryId, item.ID[:]) || request.City == "" || len(request.SessionId) != 16 {
		return nil, errors.New("lifecycle delivery identity mismatch")
	}
	return &request, nil
}

type GatewaySender struct {
	conn   *grpc.ClientConn
	client gatewayv1.LifecycleServiceClient
	secret string
}

func NewGatewaySender(address, secret string) (*GatewaySender, error) {
	if address == "" || secret == "" {
		return nil, errors.New("gateway address and lifecycle secret are required")
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create gateway client: %w", err)
	}
	return &GatewaySender{conn: conn, client: gatewayv1.NewLifecycleServiceClient(conn), secret: secret}, nil
}

func (s *GatewaySender) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close lifecycle gateway client: %w", err)
	}
	return nil
}

func (s *GatewaySender) Send(ctx context.Context, item *Item) error {
	request, err := decodeLifecycle(item)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, lifecycleSendTimeout)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-lifecycle-secret", s.secret)
	ack, err := s.client.DeliverCatalogLifecycle(ctx, request)
	if err != nil {
		return fmt.Errorf("deliver lifecycle to gateway: %w", err)
	}
	if !bytes.Equal(ack.DeliveryId, request.DeliveryId) {
		return errors.New("gateway acknowledged another lifecycle delivery")
	}
	return nil
}

type KafkaLifecycleSender struct {
	client *kgo.Client
	ready  bool
}

func NewKafkaLifecycleSender(brokers []string) (*KafkaLifecycleSender, error) {
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		return nil, fmt.Errorf("create lifecycle kafka sender: %w", err)
	}
	return &KafkaLifecycleSender{client: client}, nil
}

func (s *KafkaLifecycleSender) Close() { s.client.Close() }

func (s *KafkaLifecycleSender) Send(ctx context.Context, item *Item) error {
	request, err := decodeLifecycle(item)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, lifecycleSendTimeout)
	defer cancel()
	if !s.ready {
		result, ensureErr := kadm.NewClient(s.client).CreateTopics(ctx, lifecyclePartitions, -1, nil, LifecycleTopic)
		if ensureErr == nil {
			ensureErr = result[LifecycleTopic].Err
		}
		if ensureErr != nil && !errors.Is(ensureErr, kerr.TopicAlreadyExists) {
			return fmt.Errorf("create lifecycle topic: %w", ensureErr)
		}
		s.ready = true
	}
	sessionID, err := uuid.FromBytes(request.SessionId)
	if err != nil {
		return fmt.Errorf("invalid lifecycle session: %w", err)
	}
	key := request.City + ":" + sessionID.String()
	record := &kgo.Record{Topic: LifecycleTopic, Key: []byte(key), Value: item.Payload}
	if err := s.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("publish lifecycle to kafka: %w", err)
	}
	return nil
}
