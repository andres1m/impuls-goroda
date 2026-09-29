package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

var ErrInvalidScenarioInput = errors.New("invalid scenario input")

type BotScenario struct {
	ScenarioID        string              `json:"scenario_id"`
	Version           string              `json:"version"`
	Source            string              `json:"source"`
	PresetID          *string             `json:"preset_id,omitempty"`
	SourceText        *string             `json:"source_text,omitempty"`
	Input             BotScenarioInput    `json:"input"`
	PendingExtraction *BotScenarioInput   `json:"pending_extraction,omitempty"`
	Status            string              `json:"status"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Outcome           *BotScenarioOutcome `json:"outcome,omitempty"`
}

type BotScenarioOutcome struct {
	Status    string     `json:"status"`
	RouteIDs  []string   `json:"route_ids"`
	Conflicts []Conflict `json:"conflicts"`
	Warnings  []Warning  `json:"warnings"`
	DataMode  string     `json:"data_mode"`
	DataAsOf  *time.Time `json:"data_as_of,omitempty"`
}

type BotScenarioInput struct {
	City        *string                 `json:"city,omitempty"`
	Timezone    *string                 `json:"timezone,omitempty"`
	StartAt     *time.Time              `json:"start_at,omitempty"`
	EndAt       *time.Time              `json:"end_at,omitempty"`
	Origin      *Coordinate             `json:"origin,omitempty"`
	Destination *Coordinate             `json:"destination,omitempty"`
	Constraints *BotScenarioConstraints `json:"constraints,omitempty"`
}

type BotScenarioConstraints struct {
	InterestMask       *string            `json:"interest_mask,omitempty"`
	ExcludedCategories *[]string          `json:"excluded_categories,omitempty"`
	MovementModes      *[]string          `json:"movement_modes,omitempty"`
	LoadProfile        *string            `json:"load_profile,omitempty"`
	Budget             *BotScenarioBudget `json:"budget,omitempty"`
	BenefitPrograms    *[]string          `json:"benefit_programs,omitempty"`
	AudienceClaims     *[]AudienceClaim   `json:"audience_claims,omitempty"`
	Obligations        *[]RouteObligation `json:"obligations,omitempty"`
	SoftPreferences    *[]string          `json:"soft_preferences,omitempty"`
	LunchWindow        *LunchWindow       `json:"lunch_window,omitempty"`
	AcceptedUnknowns   *[]string          `json:"accepted_unknowns,omitempty"`
	PushkinCardOnly    *bool              `json:"pushkin_card_only,omitempty"`
	SemanticQuery      *string            `json:"semantic_query,omitempty"`
}

type BotScenarioBudget struct {
	Mode  *string `json:"mode,omitempty"`
	Limit *Money  `json:"limit,omitempty"`
}

type CompleteBotScenarioInput struct {
	ExpectedVersion int64               `json:"expected_version,string"`
	Input           ConfirmedRouteInput `json:"input"`
}

func DecodeBotScenarioInput(raw []byte) (BotScenarioInput, error) {
	var input BotScenarioInput
	if err := decodeScenarioJSON(raw, "BotScenarioInput", &input); err != nil {
		return BotScenarioInput{}, err
	}
	if err := input.validateKnownFields(); err != nil {
		return BotScenarioInput{}, err
	}
	return input, nil
}

func DecodeCompleteBotScenarioInput(raw []byte) (CompleteBotScenarioInput, error) {
	var input CompleteBotScenarioInput
	if err := decodeScenarioJSON(raw, "CompleteBotScenarioRequest", &input); err != nil || input.ExpectedVersion < 1 {
		return CompleteBotScenarioInput{}, ErrInvalidScenarioInput
	}
	encoded, err := json.Marshal(input.Input)
	if err != nil {
		return CompleteBotScenarioInput{}, ErrInvalidScenarioInput
	}
	input.Input, err = DecodeInput(encoded)
	if err != nil {
		return CompleteBotScenarioInput{}, ErrInvalidScenarioInput
	}
	return input, nil
}

func decodeScenarioJSON(raw []byte, schema string, destination any) error {
	if !utf8.Valid(raw) {
		return ErrInvalidScenarioInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return ErrInvalidScenarioInput
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidScenarioInput
	}
	if checkInput(value, inputSchemas[schema]) != nil || json.Unmarshal(raw, destination) != nil {
		return ErrInvalidScenarioInput
	}
	return nil
}

func (in BotScenarioInput) validateKnownFields() error {
	if in.City != nil && strings.TrimSpace(*in.City) == "" {
		return ErrInvalidScenarioInput
	}
	if in.Timezone != nil {
		if _, err := time.LoadLocation(*in.Timezone); err != nil {
			return ErrInvalidScenarioInput
		}
	}
	for _, instant := range []*time.Time{in.StartAt, in.EndAt} {
		if instant != nil && validInstant(*instant) != nil {
			return ErrInvalidScenarioInput
		}
	}
	if in.StartAt != nil && in.EndAt != nil && !in.EndAt.After(*in.StartAt) {
		return ErrInvalidScenarioInput
	}
	for _, point := range []*Coordinate{in.Origin, in.Destination} {
		if point != nil && (d.Coordinate{Latitude: point.Latitude, Longitude: point.Longitude}).Validate() != nil {
			return ErrInvalidScenarioInput
		}
	}
	if in.Constraints == nil {
		return nil
	}
	wire := in.Constraints
	if wire.SemanticQuery != nil && strings.TrimSpace(*wire.SemanticQuery) == "" {
		return ErrInvalidScenarioInput
	}
	if wire.LoadProfile != nil && strings.TrimSpace(*wire.LoadProfile) == "" {
		return ErrInvalidScenarioInput
	}
	for _, values := range []*[]string{wire.MovementModes, wire.BenefitPrograms, wire.SoftPreferences, wire.AcceptedUnknowns} {
		if values != nil {
			for _, value := range *values {
				if strings.TrimSpace(value) == "" {
					return ErrInvalidScenarioInput
				}
			}
		}
	}
	if budget := wire.Budget; budget != nil {
		if budget.Mode != nil && *budget.Mode == "none" && budget.Limit != nil {
			return ErrInvalidScenarioInput
		}
		if _, err := moneyProto(budget.Limit); err != nil {
			return ErrInvalidScenarioInput
		}
	}
	if wire.AudienceClaims != nil {
		for _, claim := range *wire.AudienceClaims {
			if (d.AudienceClaim{Audience: claim.Audience, Evidence: d.EvidenceKind(claim.Evidence)}).Validate() != nil {
				return ErrInvalidScenarioInput
			}
		}
	}
	if wire.Obligations != nil {
		for _, obligation := range *wire.Obligations {
			if obligation.VisitID == nil && obligation.SessionID == nil {
				return ErrInvalidScenarioInput
			}
			if obligation.StartsAt != nil && validInstant(*obligation.StartsAt) != nil {
				return ErrInvalidScenarioInput
			}
		}
	}
	if window := wire.LunchWindow; window != nil {
		if validInstant(window.StartAt) != nil || validInstant(window.EndAt) != nil ||
			(d.LunchWindow{Start: window.StartAt, End: window.EndAt, MinDurationSeconds: window.MinDurationSeconds}).Validate() != nil {
			return ErrInvalidScenarioInput
		}
	}
	return nil
}
