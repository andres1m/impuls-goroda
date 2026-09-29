package postgres

import (
	"context"
	"errors"
	"strconv"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

var ErrCatalogChanged = errors.New("catalog changed during computation")
var ErrCityTimezone = errors.New("city timezone does not match")

func (q *Queries) CheckCity(ctx context.Context, city, timezone string) error {
	var actual string
	if err := q.db.QueryRow(ctx, `SELECT timezone FROM ref.city WHERE code=$1`, city).Scan(&actual); err != nil {
		return mapQueryError("read city", err)
	}
	if actual != timezone {
		return ErrCityTimezone
	}
	return nil
}

func (q *Queries) LockCatalog(ctx context.Context, city string, expected int64) error {
	var revision int64
	if err := q.db.QueryRow(ctx, `SELECT ref.lock_city_shared($1)`, city).Scan(&revision); err != nil {
		return err
	}
	if revision != expected {
		return ErrCatalogChanged
	}
	return nil
}

func (q *Queries) CheckPlanCatalog(ctx context.Context, city string, plan d.RoutePlanSnapshot) error {
	for _, step := range plan.Steps {
		if step.Catalog == nil {
			continue
		}
		catalog := step.Catalog
		modes := make([]string, 0, len(plan.Constraints.MovementModes))
		for _, leg := range plan.Legs {
			if leg.ToVisitID != nil && *leg.ToVisitID == step.VisitID {
				modes = append(modes, string(leg.Mode))
			}
		}
		if len(modes) == 0 {
			for _, mode := range plan.Constraints.MovementModes {
				modes = append(modes, string(mode))
			}
		}
		var valid bool
		if err := q.db.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM catalog.place p WHERE p.id=$1 AND p.city=$2 AND p.is_active
AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM catalog.place_entrance e WHERE e.id=$3 AND e.city=p.city AND e.place_id=p.id AND e.accessibility_status<>'unavailable' AND e.allowed_modes && $4::text[]))
AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM catalog.event e WHERE e.id=$5 AND e.city=p.city AND e.place_id=p.id AND e.is_active)))`,
			optionalRouteID(catalog.PlaceID), city, optionalRouteID(catalog.EntranceID), modes, optionalRouteID(catalog.EventID)).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return routewire.ErrInvalidResult
		}
		if catalog.SessionID != nil {
			version, err := strconv.ParseInt(catalog.SessionVersion, 10, 64)
			if err != nil || version <= 0 {
				return routewire.ErrInvalidResult
			}
			buffer := int64(0)
			for _, obligation := range plan.Constraints.Obligations {
				if (obligation.SessionID != nil && *obligation.SessionID == *catalog.SessionID) || (obligation.VisitID != nil && *obligation.VisitID == step.VisitID) {
					buffer = max(buffer, obligation.ArrivalBufferSeconds)
				}
			}
			holdsPlace := step.Obligation && (step.Participation.Status == d.ParticipationUserReported || step.Participation.Status == d.ParticipationProviderConfirmed)
			if err = q.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalog.session s
WHERE s.id=$1 AND s.city=$2 AND s.event_id=$3 AND s.version=$4
AND s.starts_at=$5 AND s.ends_at=$6 AND s.availability_status=$7
AND s.availability_status<>'cancelled' AND (s.availability_status<>'sold_out' OR $8)
AND $9::timestamptz>=s.starts_at AND $10::timestamptz<=s.ends_at
AND $10::timestamptz-$9::timestamptz>=make_interval(secs=>s.min_duration_s)
AND $11::bigint>=s.min_duration_s
AND (s.last_entry_at IS NULL OR $9::timestamptz<=s.last_entry_at)
AND ((s.slot_type='FIXED_SESSION' AND $10::timestamptz=s.ends_at
AND (($9::timestamptz=s.starts_at AND ($9::timestamptz-$12::timestamptz>=make_interval(secs=>GREATEST(s.buffer_s,$13::integer)) OR s.late_entry_allowed IS TRUE))
OR ($9::timestamptz>s.starts_at AND s.late_entry_allowed IS TRUE AND $9::timestamptz<=COALESCE(s.last_entry_at,s.ends_at-make_interval(secs=>s.min_duration_s)))))
OR (s.slot_type='CONTINUOUS_WINDOW' AND $9::timestamptz-$12::timestamptz>=make_interval(secs=>GREATEST(s.buffer_s,$13::integer)))))`,
				optionalRouteID(catalog.SessionID), city, optionalRouteID(catalog.EventID), version, catalog.SessionStartsAt, catalog.SessionEndsAt, string(catalog.Availability), holdsPlace, step.VisitStartAt, step.VisitEndAt, step.MinDurationSeconds, step.ArrivalAt, buffer).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return routewire.ErrInvalidResult
			}
		}
		if step.Cost == nil {
			return routewire.ErrInvalidResult
		}
		cost := step.Cost
		if cost.PriceOfferID != nil {
			audiences := []string{"general"}
			for _, claim := range plan.Constraints.AudienceClaims {
				audiences = append(audiences, claim.Audience)
			}
			if err := q.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalog.price_offer p JOIN catalog.session s ON s.id=p.session_id AND s.city=p.city
WHERE p.id=$1 AND p.city=$2 AND p.session_id=$3 AND p.is_active
AND p.price_status=$4 AND p.amount_min IS NOT DISTINCT FROM $5::bigint AND p.amount_max IS NOT DISTINCT FROM $6::bigint
AND COALESCE(p.currency,$7::text)=$7 AND p.audience=$8 AND p.audience=ANY($9::text[])
AND p.eligibility_age_min IS NULL AND p.eligibility_age_max IS NULL
AND (p.valid_until IS NULL OR p.valid_until>=s.starts_at)
AND (NOT $10 OR p.price_status='unknown' OR p.amount_max=0 OR 'pushkin_card'=ANY(p.benefit_programs))
AND (NOT $11 OR p.benefit_programs && $12::text[]))`,
				optionalRouteID(cost.PriceOfferID), city, optionalRouteID(catalog.SessionID), string(cost.Price.Status), cost.Price.LowerMinor, cost.Price.UpperMinor, cost.Price.Currency, cost.Audience, audiences, plan.Constraints.PushkinCardOnly, cost.ProgramAmount != nil, plan.Constraints.BenefitPrograms).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return routewire.ErrInvalidResult
			}
		} else if catalog.SessionID != nil && cost.Price.Status == d.PriceFree {
			if err := q.db.QueryRow(ctx, `SELECT access_type='free' FROM catalog.session WHERE id=$1 AND city=$2`, optionalRouteID(catalog.SessionID), city).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return routewire.ErrInvalidResult
			}
		}
	}
	return nil
}
