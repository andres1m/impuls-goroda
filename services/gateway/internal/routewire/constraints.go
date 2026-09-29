package routewire

import (
	"errors"
	"strconv"
	"strings"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (in ConfirmedRouteInput) DomainConstraints() (d.RouteConstraints, error) {
	wire := in.Constraints
	mask, err := strconv.ParseUint(strings.TrimPrefix(wire.InterestMask, "0x"), 16, 64)
	if err != nil {
		return d.RouteConstraints{}, errors.New("invalid interest mask")
	}
	out := d.RouteConstraints{
		InterestMask: mask, ExcludedCategories: append([]string(nil), wire.ExcludedCategories...),
		LoadProfile: wire.LoadProfile, BenefitPrograms: append([]string(nil), wire.BenefitPrograms...),
		SoftPreferences: append([]string(nil), wire.SoftPreferences...),
		Budget:          d.Budget{Mode: d.BudgetMode(wire.Budget.Mode)},
	}
	if wire.PushkinCardOnly != nil {
		out.PushkinCardOnly = *wire.PushkinCardOnly
	}
	if wire.SemanticQuery != nil {
		out.SemanticQuery = *wire.SemanticQuery
	}
	if wire.Budget.Limit != nil {
		limit, err := moneyProto(wire.Budget.Limit)
		if err != nil {
			return d.RouteConstraints{}, err
		}
		out.Budget.Limit = &d.Money{AmountMinor: limit.AmountMinor, Currency: limit.Currency}
	}
	for _, mode := range wire.MovementModes {
		out.MovementModes = append(out.MovementModes, d.MovementMode(mode))
	}
	for _, unknown := range wire.AcceptedUnknowns {
		out.AcceptedUnknowns = append(out.AcceptedUnknowns, d.UnknownConditionCode(unknown))
	}
	for _, claim := range wire.AudienceClaims {
		out.AudienceClaims = append(out.AudienceClaims, d.AudienceClaim{Audience: claim.Audience, Evidence: d.EvidenceKind(claim.Evidence)})
	}
	for _, obligation := range wire.Obligations {
		mapped := d.RouteObligation{
			ArrivalBufferSeconds: obligation.ArrivalBufferSeconds,
			Participation:        d.ParticipationStatus(obligation.Participation),
		}
		if obligation.VisitID != nil {
			id, err := uuid.Parse(*obligation.VisitID)
			if err != nil || id == uuid.Nil {
				return d.RouteConstraints{}, errors.New("invalid obligation visit")
			}
			visit := d.VisitID(id)
			mapped.VisitID = &visit
		}
		if obligation.SessionID != nil {
			id, err := uuid.Parse(*obligation.SessionID)
			if err != nil || id == uuid.Nil {
				return d.RouteConstraints{}, errors.New("invalid obligation session")
			}
			session := d.EventSessionID(id)
			mapped.SessionID = &session
		}
		if obligation.StartsAt != nil {
			instant := obligation.StartsAt.UTC()
			if err := validInstant(instant); err != nil {
				return d.RouteConstraints{}, err
			}
			mapped.StartsAt = &instant
		}
		out.Obligations = append(out.Obligations, mapped)
	}
	if window := wire.LunchWindow; window != nil {
		if err := validInstant(window.StartAt); err != nil {
			return d.RouteConstraints{}, err
		}
		if err := validInstant(window.EndAt); err != nil {
			return d.RouteConstraints{}, err
		}
		if window.MinDurationSeconds <= 0 || window.MinDurationSeconds > int64(window.EndAt.Sub(window.StartAt)/time.Second) {
			return d.RouteConstraints{}, errors.New("invalid lunch duration")
		}
		out.LunchWindow = &d.LunchWindow{Start: window.StartAt.UTC(), End: window.EndAt.UTC(), MinDurationSeconds: window.MinDurationSeconds}
	}
	return out, out.Validate()
}

func validInstant(instant time.Time) error {
	if instant.IsZero() || timestamppb.New(instant).CheckValid() != nil {
		return errors.New("invalid timestamp")
	}
	return nil
}
