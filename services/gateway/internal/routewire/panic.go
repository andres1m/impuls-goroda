package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"time"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrInvalidPanicInput = errors.New("invalid panic input")

type PanicInput struct {
	RouteID          string     `json:"route_id"`
	DelayMode        string     `json:"delay_mode"`
	Position         Coordinate `json:"position"`
	PositionSource   string     `json:"position_source"`
	EffectiveStartAt *time.Time `json:"effective_start_at,omitempty"`
	DelaySeconds     *int64     `json:"delay_seconds,omitempty"`
}

func DecodePanicInput(raw []byte) (PanicInput, error) {
	if !utf8.Valid(raw) {
		return PanicInput{}, ErrInvalidPanicInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return PanicInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return PanicInput{}, ErrInvalidPanicInput
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 5 {
		return PanicInput{}, ErrInvalidPanicInput
	}
	for key := range fields {
		switch key {
		case "route_id", "delay_mode", "position", "position_source", "effective_start_at", "delay_seconds":
		default:
			return PanicInput{}, ErrInvalidPanicInput
		}
	}
	input := PanicInput{}
	input.RouteID, ok = fields["route_id"].(string)
	if !ok {
		return PanicInput{}, ErrInvalidPanicInput
	}
	input.DelayMode, ok = fields["delay_mode"].(string)
	if !ok {
		return PanicInput{}, ErrInvalidPanicInput
	}
	input.PositionSource, ok = fields["position_source"].(string)
	if !ok {
		return PanicInput{}, ErrInvalidPanicInput
	}
	position, ok := fields["position"].(map[string]any)
	if !ok || len(position) != 2 {
		return PanicInput{}, ErrInvalidPanicInput
	}
	lat, ok := position["latitude"].(json.Number)
	if !ok {
		return PanicInput{}, ErrInvalidPanicInput
	}
	lon, ok := position["longitude"].(json.Number)
	if !ok {
		return PanicInput{}, ErrInvalidPanicInput
	}
	input.Position.Latitude, err = lat.Float64()
	if err != nil {
		return PanicInput{}, ErrInvalidPanicInput
	}
	input.Position.Longitude, err = lon.Float64()
	if err != nil {
		return PanicInput{}, ErrInvalidPanicInput
	}
	switch input.DelayMode {
	case "already_delayed":
		text, ok := fields["effective_start_at"].(string)
		if !ok {
			return PanicInput{}, ErrInvalidPanicInput
		}
		instant, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return PanicInput{}, ErrInvalidPanicInput
		}
		input.EffectiveStartAt = &instant
	case "future_wait":
		number, ok := fields["delay_seconds"].(json.Number)
		if !ok {
			return PanicInput{}, ErrInvalidPanicInput
		}
		seconds, err := number.Float64()
		if err != nil || seconds < 1 || seconds > math.MaxInt32 || math.Trunc(seconds) != seconds {
			return PanicInput{}, ErrInvalidPanicInput
		}
		value := int64(seconds)
		input.DelaySeconds = &value
	default:
		return PanicInput{}, ErrInvalidPanicInput
	}
	id, err := input.routeID()
	if err != nil || input.validate() != nil {
		return PanicInput{}, ErrInvalidPanicInput
	}
	input.RouteID = uuid.UUID(id).String()
	return input, nil
}

func (in PanicInput) routeID() (d.RouteID, error) {
	id, err := uuid.Parse(in.RouteID)
	if err != nil || id == uuid.Nil || len(in.RouteID) != 36 {
		return d.RouteID{}, ErrInvalidPanicInput
	}
	return d.RouteID(id), nil
}

func (in PanicInput) validate() error {
	if _, err := in.routeID(); err != nil {
		return err
	}
	if in.PositionSource != "device" && in.PositionSource != "manual" {
		return ErrInvalidPanicInput
	}
	if (d.Coordinate{Latitude: in.Position.Latitude, Longitude: in.Position.Longitude}).Validate() != nil {
		return ErrInvalidPanicInput
	}
	switch in.DelayMode {
	case "already_delayed":
		if in.EffectiveStartAt == nil || in.EffectiveStartAt.IsZero() || in.DelaySeconds != nil || timestamppb.New(*in.EffectiveStartAt).CheckValid() != nil {
			return ErrInvalidPanicInput
		}
	case "future_wait":
		if in.EffectiveStartAt != nil || in.DelaySeconds == nil || *in.DelaySeconds < 1 || *in.DelaySeconds > math.MaxInt32 {
			return ErrInvalidPanicInput
		}
	default:
		return ErrInvalidPanicInput
	}
	return nil
}

func (in PanicInput) Proto(now time.Time) (*pb.DelayTrigger, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	source := pb.PositionSource_POSITION_SOURCE_DEVICE
	if in.PositionSource == "manual" {
		source = pb.PositionSource_POSITION_SOURCE_MANUAL
	}
	mode := pb.DelayMode_DELAY_MODE_ALREADY_DELAYED
	var effective time.Time
	if in.DelayMode == "already_delayed" {
		effective = *in.EffectiveStartAt
	} else {
		if now.IsZero() {
			return nil, ErrInvalidPanicInput
		}
		mode = pb.DelayMode_DELAY_MODE_FUTURE_WAIT
		effective = now.Add(time.Duration(*in.DelaySeconds) * time.Second)
	}
	timestamp := timestamppb.New(effective)
	if timestamp.CheckValid() != nil {
		return nil, ErrInvalidPanicInput
	}
	return &pb.DelayTrigger{Mode: mode, EffectiveStartAt: timestamp, Position: coordinateProto(in.Position), PositionSource: source}, nil
}

func BuildPanicRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, input PanicInput, now time.Time) (*pb.RecomputeRequest, error) {
	id, err := input.routeID()
	if err != nil || id != routeID {
		return nil, ErrInvalidPanicInput
	}
	trigger, err := input.Proto(now)
	if err != nil {
		return nil, err
	}
	request, err := buildRecomputeBase(routeID, city, base, history)
	if err != nil {
		return nil, err
	}
	request.Trigger = &pb.RecomputeRequest_Delay{Delay: trigger}
	return request, nil
}
