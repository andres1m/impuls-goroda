package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var inputSchemas = func() map[string]map[string]any {
	var schemas map[string]map[string]any
	if err := json.Unmarshal([]byte(inputSchemaJSON), &schemas); err != nil {
		panic(err)
	}
	return schemas
}()

func DecodeInput(data []byte) (ConfirmedRouteInput, error) {
	if !utf8.Valid(data) {
		return ConfirmedRouteInput{}, errors.New("invalid JSON encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return ConfirmedRouteInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ConfirmedRouteInput{}, errors.New("unexpected trailing JSON")
	}
	if err := checkInput(value, inputSchemas["ConfirmedRouteInput"]); err != nil {
		return ConfirmedRouteInput{}, err
	}
	var input ConfirmedRouteInput
	if err := json.Unmarshal(data, &input); err != nil {
		return input, err
	}
	if _, err := input.Proto(); err != nil {
		return input, err
	}
	for _, items := range [][]string{input.Constraints.AcceptedUnknowns, input.Constraints.BenefitPrograms, input.Constraints.ExcludedCategories, input.Constraints.MovementModes, input.Constraints.SoftPreferences} {
		sort.Strings(items)
	}
	sort.Slice(input.Constraints.AudienceClaims, func(i, j int) bool {
		return input.Constraints.AudienceClaims[i].Audience < input.Constraints.AudienceClaims[j].Audience
	})
	if input.Constraints.PushkinCardOnly == nil {
		value := false
		input.Constraints.PushkinCardOnly = &value
	}
	input.Constraints.InterestMask = strings.ToLower(input.Constraints.InterestMask)
	input.StartAt = input.StartAt.UTC()
	input.EndAt = input.EndAt.UTC()
	if window := input.Constraints.LunchWindow; window != nil {
		window.StartAt = window.StartAt.UTC()
		window.EndAt = window.EndAt.UTC()
	}
	for i := range input.Constraints.Obligations {
		obligation := &input.Constraints.Obligations[i]
		for _, id := range []*string{obligation.VisitID, obligation.SessionID} {
			if id != nil {
				*id = strings.ToLower(*id)
			}
		}
		if obligation.StartsAt != nil {
			normalized := obligation.StartsAt.UTC()
			obligation.StartsAt = &normalized
		}
	}
	return input, nil
}

func readJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		out := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name := key.(string)
			if _, ok := out[name]; ok {
				return nil, errors.New("duplicate JSON field")
			}
			value, err := readJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
		_, err = decoder.Token()
		return out, err
	case json.Delim('['):
		out := []any{}
		for decoder.More() {
			value, err := readJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		_, err = decoder.Token()
		return out, err
	default:
		return token, nil
	}
}

func checkInput(value any, schema map[string]any) error {
	bad := errors.New("input does not match the route contract")
	if schema == nil {
		return bad
	}
	if ref, ok := schema["$ref"].(string); ok {
		return checkInput(value, inputSchemas[ref[strings.LastIndex(ref, "/")+1:]])
	}
	if value == nil {
		return bad
	}
	if fixed, ok := schema["const"]; ok && value != fixed {
		return bad
	}
	if choices, ok := schema["enum"].([]any); ok {
		found := false
		for _, choice := range choices {
			if value == choice {
				found = true
			}
		}
		if !found {
			return bad
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return bad
		}
		props := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, ok := object[key.(string)]; !ok {
					return bad
				}
			}
		}
		for key, field := range object {
			spec, ok := props[key]
			if !ok {
				return bad
			}
			if err := checkInput(field, spec.(map[string]any)); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return bad
		}
		if min, ok := schema["minItems"].(float64); ok && float64(len(items)) < min {
			return bad
		}
		if max, ok := schema["maxItems"].(float64); ok && float64(len(items)) > max {
			return bad
		}
		seen := map[string]bool{}
		for _, item := range items {
			if err := checkInput(item, schema["items"].(map[string]any)); err != nil {
				return err
			}
			if schema["uniqueItems"] == true {
				b, _ := json.Marshal(item)
				if seen[string(b)] {
					return bad
				}
				seen[string(b)] = true
			}
		}
	case "string":
		str, ok := value.(string)
		if !ok {
			return bad
		}
		n := float64(utf8.RuneCountInString(str))
		if min, ok := schema["minLength"].(float64); ok && n < min {
			return bad
		}
		if max, ok := schema["maxLength"].(float64); ok && n > max {
			return bad
		}
		if pattern, ok := schema["pattern"].(string); ok {
			match, err := regexp.MatchString(pattern, str)
			if err != nil || !match {
				return bad
			}
		}
		switch schema["format"] {
		case "date-time":
			if _, err := time.Parse(time.RFC3339Nano, str); err != nil {
				return bad
			}
		case "uuid":
			id, err := uuid.Parse(str)
			if err != nil || id == uuid.Nil || len(str) != 36 {
				return bad
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return bad
		}
	case "integer", "number":
		number, ok := value.(json.Number)
		if !ok {
			return bad
		}
		v, err := number.Float64()
		if err != nil {
			return bad
		}
		if schema["type"] == "integer" {
			if _, err := number.Int64(); err != nil {
				return bad
			}
		}
		if min, ok := schema["minimum"].(float64); ok && v < min {
			return bad
		}
		if max, ok := schema["maximum"].(float64); ok && v > max {
			return bad
		}
		if schema["format"] == "int32" && (v > 2147483647 || v < -2147483648) {
			return bad
		}
	}
	return nil
}

func moneyProto(m *Money) (*pb.Money, error) {
	if m == nil {
		return nil, nil
	}
	n, err := strconv.ParseInt(m.AmountMinor, 10, 64)
	if err != nil || n < 0 {
		return nil, errors.New("invalid money amount")
	}
	return &pb.Money{AmountMinor: n, Currency: m.Currency}, nil
}
func coordinateProto(c Coordinate) *pb.Coordinate {
	return &pb.Coordinate{Latitude: c.Latitude, Longitude: c.Longitude}
}
func optionalUUID(id *string) ([]byte, error) {
	if id == nil {
		return nil, nil
	}
	parsed, err := uuid.Parse(*id)
	if err != nil || parsed == uuid.Nil {
		return nil, errors.New("invalid identifier")
	}
	return append([]byte(nil), parsed[:]...), nil
}
func optionalTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func (in ConfirmedRouteInput) Proto() (*pb.OptimizeRequest, error) {
	if strings.TrimSpace(in.City) == "" || in.Timezone == "" || in.StartAt.IsZero() || !in.EndAt.After(in.StartAt) {
		return nil, errors.New("invalid planning interval")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return nil, errors.New("invalid timezone")
	}
	if _, err := in.DomainConstraints(); err != nil {
		return nil, err
	}
	for _, instant := range []time.Time{in.StartAt, in.EndAt} {
		if err := timestamppb.New(instant).CheckValid(); err != nil {
			return nil, errors.New("invalid planning timestamp")
		}
	}
	if err := (d.Coordinate{Longitude: in.Origin.Longitude, Latitude: in.Origin.Latitude}).Validate(); err != nil {
		return nil, err
	}
	mask, err := strconv.ParseUint(strings.TrimPrefix(in.Constraints.InterestMask, "0x"), 16, 64)
	if err != nil {
		return nil, errors.New("invalid interest mask")
	}
	limit, err := moneyProto(in.Constraints.Budget.Limit)
	if err != nil {
		return nil, err
	}
	budgetModes := map[string]pb.BudgetMode{"none": pb.BudgetMode_BUDGET_MODE_NONE, "advisory": pb.BudgetMode_BUDGET_MODE_ADVISORY, "strict": pb.BudgetMode_BUDGET_MODE_STRICT}
	mode, ok := budgetModes[in.Constraints.Budget.Mode]
	if !ok || (mode == pb.BudgetMode_BUDGET_MODE_NONE) != (limit == nil) {
		return nil, errors.New("invalid budget")
	}
	constraints := &pb.RouteConstraints{InterestMask: mask, MovementModes: in.Constraints.MovementModes, LoadProfile: in.Constraints.LoadProfile, Budget: &pb.Budget{Mode: mode, Limit: limit}, BenefitPrograms: in.Constraints.BenefitPrograms, SoftPreferences: in.Constraints.SoftPreferences, AcceptedUnknowns: in.Constraints.AcceptedUnknowns}
	if in.Constraints.PushkinCardOnly != nil {
		constraints.PushkinCardOnly = *in.Constraints.PushkinCardOnly
	}
	if in.Constraints.SemanticQuery != nil {
		constraints.SemanticQuery = *in.Constraints.SemanticQuery
	}
	for _, category := range in.Constraints.ExcludedCategories {
		number, ok := pb.Category_value["CATEGORY_"+strings.ToUpper(category)]
		if !ok || number == 0 {
			return nil, errors.New("invalid category")
		}
		constraints.ExcludedCategories = append(constraints.ExcludedCategories, pb.Category(number))
	}
	for _, claim := range in.Constraints.AudienceClaims {
		if claim.Evidence != "user_reported" {
			return nil, errors.New("invalid audience evidence")
		}
		constraints.AudienceClaims = append(constraints.AudienceClaims, claim.Audience)
	}
	for _, obligation := range in.Constraints.Obligations {
		visit, err := optionalUUID(obligation.VisitID)
		if err != nil {
			return nil, err
		}
		session, err := optionalUUID(obligation.SessionID)
		if err != nil {
			return nil, err
		}
		if len(visit) == 0 && len(session) == 0 {
			return nil, errors.New("obligation reference is required")
		}
		number, ok := pb.ParticipationStatus_value["PARTICIPATION_STATUS_"+strings.ToUpper(obligation.Participation)]
		if !ok || number == 0 {
			return nil, errors.New("invalid participation")
		}
		constraints.Obligations = append(constraints.Obligations, &pb.RouteObligation{VisitId: visit, SessionId: session, StartsAt: optionalTimestamp(obligation.StartsAt), ArrivalBufferSeconds: obligation.ArrivalBufferSeconds, Participation: pb.ParticipationStatus(number)})
	}
	if window := in.Constraints.LunchWindow; window != nil {
		w := d.LunchWindow{Start: window.StartAt, End: window.EndAt, MinDurationSeconds: window.MinDurationSeconds}
		if err := w.Validate(); err != nil {
			return nil, err
		}
		constraints.LunchWindow = &pb.LunchWindow{StartAt: timestamppb.New(w.Start), EndAt: timestamppb.New(w.End), MinDurationSeconds: w.MinDurationSeconds}
	}
	request := &pb.OptimizeRequest{City: in.City, Timezone: in.Timezone, StartAt: timestamppb.New(in.StartAt), EndAt: timestamppb.New(in.EndAt), Origin: coordinateProto(in.Origin), Constraints: constraints}
	if in.Destination != nil {
		if err := (d.Coordinate{Longitude: in.Destination.Longitude, Latitude: in.Destination.Latitude}).Validate(); err != nil {
			return nil, err
		}
		request.Destination = coordinateProto(*in.Destination)
	}
	return request, nil
}
