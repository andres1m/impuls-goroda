package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LunchProposalInput contains only client choices. Provider facts are resolved by
// the gateway after authentication and are never accepted from this JSON body.
type LunchProposalInput struct {
	Action                        string            `json:"action"`
	LunchID                       *d.VisitID        `json:"lunch_id,omitempty"`
	Placement                     *LunchPlacement   `json:"placement,omitempty"`
	Venue                         *LunchVenueChoice `json:"venue,omitempty"`
	AcknowledgeExternalCommitment *bool             `json:"acknowledge_external_commitment,omitempty"`
}

type LunchPlacement struct {
	AfterVisitID    d.VisitID `json:"after_visit_id"`
	DurationSeconds int64     `json:"duration_seconds"`
}

type LunchVenueChoice struct {
	Provider       string     `json:"provider,omitempty"`
	ExternalID     string     `json:"external_id,omitempty"`
	CatalogVisitID *d.VisitID `json:"catalog_visit_id,omitempty"`
}

func DecodeLunchProposalInput(raw []byte) (LunchProposalInput, error) {
	if !utf8.Valid(raw) {
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := readJSON(dec, 0)
	if err != nil {
		return LunchProposalInput{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	action, ok := fields["action"].(string)
	if !ok {
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	input := LunchProposalInput{Action: action}
	allowed := map[string]bool{"action": true}
	switch action {
	case "add", "update":
		allowed["placement"] = true
		allowed["venue"] = true
		p, ok := fields["placement"].(map[string]any)
		if !ok || len(p) != 2 {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		anchor, ok := parseLunchID(p["after_visit_id"])
		if !ok {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		seconds, ok := p["duration_seconds"].(json.Number)
		if !ok {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		duration, err := seconds.Int64()
		if err != nil || duration < 2700 || duration > 3600 {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		input.Placement = &LunchPlacement{AfterVisitID: anchor, DurationSeconds: duration}
		if rawVenue, present := fields["venue"]; present {
			v, ok := rawVenue.(map[string]any)
			if !ok {
				return LunchProposalInput{}, ErrInvalidRecomputeInput
			}
			choice := &LunchVenueChoice{}
			if len(v) == 2 {
				provider, one := v["provider"].(string)
				external, two := v["external_id"].(string)
				if !one || !two || provider != "2gis" || external == "" || strings.TrimSpace(external) != external || len(external) > 128 {
					return LunchProposalInput{}, ErrInvalidRecomputeInput
				}
				for _, character := range external {
					if unicode.IsControl(character) {
						return LunchProposalInput{}, ErrInvalidRecomputeInput
					}
				}
				choice.Provider, choice.ExternalID = provider, external
			} else if len(v) == 1 {
				id, ok := parseLunchID(v["catalog_visit_id"])
				if !ok {
					return LunchProposalInput{}, ErrInvalidRecomputeInput
				}
				choice.CatalogVisitID = &id
			} else {
				return LunchProposalInput{}, ErrInvalidRecomputeInput
			}
			input.Venue = choice
		}
	case "remove":
	default:
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	if action != "add" {
		allowed["lunch_id"] = true
		allowed["acknowledge_external_commitment"] = true
		id, ok := parseLunchID(fields["lunch_id"])
		if !ok {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		input.LunchID = &id
		ack, ok := fields["acknowledge_external_commitment"].(bool)
		if !ok {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
		input.AcknowledgeExternalCommitment = &ack
	}
	if len(fields) < len(allowed)-1 {
		return LunchProposalInput{}, ErrInvalidRecomputeInput
	}
	for field := range fields {
		if !allowed[field] {
			return LunchProposalInput{}, ErrInvalidRecomputeInput
		}
	}
	return input, nil
}

func parseLunchID(value any) (d.VisitID, bool) {
	str, ok := value.(string)
	if !ok {
		return d.VisitID{}, false
	}
	id, err := uuid.Parse(str)
	return d.VisitID(id), err == nil && id != uuid.Nil && id.String() == str
}

type LunchIntent struct {
	Input    LunchProposalInput
	NewID    d.VisitID
	External *d.ExternalVenueSnapshot
}

func BuildLunchRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, intent LunchIntent) (*pb.RecomputeRequest, error) {
	request, err := buildRecomputeBase(routeID, city, base, history)
	if err != nil {
		return nil, err
	}
	steps := make(map[d.VisitID]d.RouteStep, len(base.Steps))
	for _, step := range base.Steps {
		steps[step.VisitID] = step
	}
	stops := make([]*pb.LunchStop, 0)
	for _, step := range base.Steps {
		if step.Lunch == nil {
			continue
		}
		stop := &pb.LunchStop{VisitId: rawID(&step.VisitID), AfterVisitId: rawID(&step.Lunch.AfterVisitID), DurationSeconds: step.Lunch.DurationSeconds}
		if step.ExternalVenue != nil {
			stop.Venue = &pb.LunchStop_External{External: lunchExternalProto(*step.ExternalVenue)}
		}
		if step.Catalog != nil {
			stop.Venue = &pb.LunchStop_CatalogVisitId{CatalogVisitId: rawID(&step.VisitID)}
		}
		stops = append(stops, stop)
	}
	input := intent.Input
	if input.Action != "add" && input.Action != "update" && input.Action != "remove" {
		return nil, ErrInvalidRecomputeInput
	}
	if intent.External != nil && intent.External.Validate() != nil {
		return nil, ErrInvalidRecomputeInput
	}
	if input.Action == "remove" && (input.Placement != nil || input.Venue != nil) {
		return nil, ErrInvalidRecomputeInput
	}
	if input.Action == "add" && (intent.NewID == (d.VisitID{}) || steps[intent.NewID].VisitID != (d.VisitID{})) {
		return nil, ErrInvalidRecomputeInput
	}
	if input.Action != "add" {
		if input.LunchID == nil {
			return nil, ErrInvalidRecomputeInput
		}
		selected, ok := steps[*input.LunchID]
		if !ok || selected.Lunch == nil {
			return nil, ErrInvalidRecomputeInput
		}
		for _, execution := range history {
			if execution.VisitID == *input.LunchID && execution.Status != d.ExecutionPlanned {
				return nil, ErrInvalidRecomputeInput
			}
		}
		if input.AcknowledgeExternalCommitment == nil || !*input.AcknowledgeExternalCommitment && (selected.Participation.Status == d.ParticipationProviderConfirmed || selected.Participation.Status == d.ParticipationUserReported) {
			return nil, ErrExternalCommitmentAcknowledgementRequired
		}
	}
	if input.Action == "remove" {
		for i, stop := range stops {
			if bytes.Equal(stop.VisitId, input.LunchID[:]) {
				stops = append(stops[:i], stops[i+1:]...)
				break
			}
		}
	} else {
		if input.Placement == nil {
			return nil, ErrInvalidRecomputeInput
		}
		anchor, ok := steps[input.Placement.AfterVisitID]
		if !ok || (input.Action == "update" && anchor.VisitID == *input.LunchID) || input.Placement.DurationSeconds < 2700 || input.Placement.DurationSeconds > 3600 {
			return nil, ErrInvalidRecomputeInput
		}
		id := intent.NewID
		if input.Action == "update" {
			id = *input.LunchID
		}
		stop := &pb.LunchStop{VisitId: rawID(&id), AfterVisitId: rawID(&input.Placement.AfterVisitID), DurationSeconds: input.Placement.DurationSeconds}
		if input.Venue != nil {
			if input.Venue.Provider == "2gis" {
				if intent.External == nil || intent.External.Provider != "2gis" || intent.External.ExternalID != input.Venue.ExternalID {
					return nil, ErrInvalidRecomputeInput
				}
				stop.Venue = &pb.LunchStop_External{External: lunchExternalProto(*intent.External)}
			} else if input.Venue.CatalogVisitID != nil {
				catalog, ok := steps[*input.Venue.CatalogVisitID]
				if !ok || input.Action != "update" || *input.Venue.CatalogVisitID != id || catalog.Lunch == nil || catalog.Catalog == nil || catalog.Catalog.Category != "gastro" {
					return nil, ErrInvalidRecomputeInput
				}
				stop.Venue = &pb.LunchStop_CatalogVisitId{CatalogVisitId: rawID(input.Venue.CatalogVisitID)}
			} else {
				return nil, ErrInvalidRecomputeInput
			}
		}
		if input.Action == "add" {
			stops = append(stops, stop)
		} else {
			for i, previous := range stops {
				if bytes.Equal(previous.VisitId, id[:]) {
					stops[i] = stop
					break
				}
			}
		}
	}
	request.Trigger = &pb.RecomputeRequest_Lunch{Lunch: &pb.LunchTrigger{SchemaVersion: 1, Stops: stops}}
	return request, nil
}

func lunchExternalProto(value d.ExternalVenueSnapshot) *pb.ExternalVenueSnapshot {
	return &pb.ExternalVenueSnapshot{Provider: value.Provider, ExternalId: value.ExternalID, Title: value.Title, Address: value.Address,
		Position: &pb.Coordinate{Longitude: value.Position.Longitude, Latitude: value.Position.Latitude}, ObservedAt: timestamppb.New(value.ObservedAt),
		Price:        &pb.Price{Status: pb.PriceStatus_PRICE_STATUS_UNKNOWN, Currency: value.Price.Currency},
		Availability: pb.ExternalVenueAvailability_EXTERNAL_VENUE_AVAILABILITY_UNKNOWN, HoursVerification: pb.VerificationStatus_VERIFICATION_STATUS_UNKNOWN}
}

func DecodeCheckedLunchRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, intent LunchIntent, response *pb.RecomputeResponse) (RecomputedResult, error) {
	if _, err := BuildLunchRecompute(routeID, city, base, history, intent); err != nil {
		return RecomputedResult{}, err
	}
	result, err := DecodeRecomputeResult(city, base, response)
	if err != nil {
		return RecomputedResult{}, err
	}
	switch result.Diagnostics.Status {
	case "PROPOSED":
		if result.Candidate == nil || ValidateLunchCandidate(base, *result.Candidate, history, intent, result.Changes) != nil {
			return RecomputedResult{}, ErrInvalidResult
		}
	case "UNCHANGED":
		// Only a genuinely identical update is an unchanged computation.
		if intent.Input.Action != "update" || intent.Input.LunchID == nil || intent.Input.Placement == nil {
			return RecomputedResult{}, ErrInvalidResult
		}
		found := false
		for _, step := range base.Steps {
			if step.VisitID == *intent.Input.LunchID && step.Lunch != nil && step.Lunch.AfterVisitID == intent.Input.Placement.AfterVisitID && step.Lunch.DurationSeconds == intent.Input.Placement.DurationSeconds {
				if intent.Input.Venue == nil && step.Kind == d.VisitFreeTime && step.ExternalVenue == nil ||
					intent.Input.Venue != nil && intent.Input.Venue.Provider == "2gis" && step.ExternalVenue != nil && step.ExternalVenue.ExternalID == intent.Input.Venue.ExternalID ||
					intent.Input.Venue != nil && intent.Input.Venue.CatalogVisitID != nil && *intent.Input.Venue.CatalogVisitID == step.VisitID && step.Catalog != nil {
					found = true
				}
			}
		}
		if !found {
			return RecomputedResult{}, ErrInvalidResult
		}
	case "CONFLICT":
		result.Candidate = nil
	default:
		return RecomputedResult{}, ErrInvalidResult
	}
	return result, nil
}

func ValidateLunchCandidate(base, candidate d.RoutePlanSnapshot, history []d.Execution, intent LunchIntent, changes []RecomputedChange) error {
	if base.Validate() != nil || candidate.Validate() != nil || !connectedPlan(candidate) ||
		base.SchemaVersion != candidate.SchemaVersion || base.Lifecycle != candidate.Lifecycle || base.ArchetypeID != candidate.ArchetypeID || base.Timezone != candidate.Timezone ||
		!base.StartAt.Equal(candidate.StartAt) || !base.EndAt.Equal(candidate.EndAt) || base.Origin != candidate.Origin || !reflect.DeepEqual(base.Destination, candidate.Destination) ||
		!reflect.DeepEqual(base.Constraints, candidate.Constraints) || candidate.CatalogRevision < base.CatalogRevision || len(candidate.Conflicts) != 0 {
		return ErrInvalidResult
	}
	before := map[d.VisitID]d.RouteStep{}
	after := map[d.VisitID]d.RouteStep{}
	skipped := map[d.VisitID]bool{}
	for _, step := range base.Steps {
		before[step.VisitID] = step
	}
	for _, step := range candidate.Steps {
		after[step.VisitID] = step
	}
	for _, execution := range history {
		old, ok := before[execution.VisitID]
		if !ok {
			return ErrInvalidRecomputeInput
		}
		next, present := after[execution.VisitID]
		switch execution.Status {
		case d.ExecutionCompleted:
			if !present || !reflect.DeepEqual(old, next) {
				return ErrInvalidResult
			}
		case d.ExecutionSkipped:
			skipped[execution.VisitID] = true
			if present {
				return ErrInvalidResult
			}
		}
	}
	for id, old := range before {
		if skipped[id] {
			continue
		}
		next, present := after[id]
		if old.Lunch == nil {
			if !present && (old.Pinned || old.Obligation || hasExternalCommitment(old) || hasConstraintObligation(base.Constraints, old)) {
				return ErrInvalidResult
			}
			if !present && !explainsRemoval(changes, id) {
				return ErrInvalidResult
			}
			if present && (!sameVisitIdentity(old, next) || old.Pinned != next.Pinned || old.Obligation != next.Obligation || old.Participation != next.Participation) {
				return ErrInvalidResult
			}
			continue
		}
		if intent.Input.Action == "remove" && intent.Input.LunchID != nil && id == *intent.Input.LunchID {
			if present {
				return ErrInvalidResult
			}
		} else if !present || next.Lunch == nil || next.Pinned != old.Pinned || next.Obligation != old.Obligation || next.Participation != old.Participation ||
			(intent.Input.LunchID == nil || id != *intent.Input.LunchID) && (!reflect.DeepEqual(next.Lunch, old.Lunch) || !reflect.DeepEqual(next.ExternalVenue, old.ExternalVenue)) {
			return ErrInvalidResult
		}
	}
	for id, next := range after {
		if _, exists := before[id]; exists {
			continue
		}
		if next.Lunch != nil && (intent.Input.Action != "add" || id != intent.NewID) {
			return ErrInvalidResult
		}
		if next.Lunch == nil && (next.Pinned || next.Obligation || hasExternalCommitment(next)) {
			return ErrInvalidResult
		}
	}
	input := intent.Input
	if input.Action == "add" || input.Action == "update" {
		id := intent.NewID
		if input.Action == "update" {
			id = *input.LunchID
		}
		step, ok := after[id]
		if !ok || step.Lunch == nil || input.Placement == nil || step.Lunch.AfterVisitID != input.Placement.AfterVisitID || step.Lunch.DurationSeconds != input.Placement.DurationSeconds {
			return ErrInvalidResult
		}
		if input.Venue == nil && (step.Kind != d.VisitFreeTime || step.ExternalVenue != nil || step.Catalog != nil) {
			return ErrInvalidResult
		}
		if input.Venue != nil && input.Venue.CatalogVisitID != nil && (step.Kind != d.VisitPlace || step.Catalog == nil || step.ExternalVenue != nil) {
			return ErrInvalidResult
		}
		if input.Venue != nil && input.Venue.CatalogVisitID != nil && !reflect.DeepEqual(step.Catalog, before[id].Catalog) {
			return ErrInvalidResult
		}
		if input.Venue != nil && input.Venue.Provider == "2gis" && step.Kind != d.VisitExternalLunch {
			return ErrInvalidResult
		}
		if input.Venue != nil && input.Venue.Provider == "2gis" && !reflect.DeepEqual(step.ExternalVenue, intent.External) {
			return ErrInvalidResult
		}
	}
	if len(changes) == 0 {
		return ErrInvalidResult
	}
	return nil
}
