package postgres

import (
	"context"
	"encoding/json"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (q *Queries) insertLunchSteps(ctx context.Context, route any, revision d.RouteRevisionNumber, city string, plan d.RoutePlanSnapshot, wire routewire.RoutePlan) error {
	for i, step := range plan.Steps {
		if step.Lunch == nil {
			continue
		}
		venueKind := "free_time"
		var place, entrance, event, session, offer, external any
		if step.ExternalVenue != nil {
			venueKind = "external"
			payload, err := json.Marshal(wire.Steps[i].ExternalVenue)
			if err != nil {
				return err
			}
			external = payload
		} else if step.Catalog != nil {
			venueKind = "catalog"
			place, entrance, event, session = optionalRouteID(step.Catalog.PlaceID), optionalRouteID(step.Catalog.EntranceID), optionalRouteID(step.Catalog.EventID), optionalRouteID(step.Catalog.SessionID)
			if step.Cost != nil {
				offer = optionalRouteID(step.Cost.PriceOfferID)
			}
		}
		_, err := q.db.Exec(ctx, `INSERT INTO planning.route_lunch_step
(route_id,revision,visit_id,after_visit_id,duration_seconds,venue_kind,city,place_id,entrance_id,event_id,session_id,price_offer_id,external_snapshot)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, route, int64(revision), encodeUUID([16]byte(step.VisitID)),
			encodeUUID([16]byte(step.Lunch.AfterVisitID)), step.Lunch.DurationSeconds, venueKind, city, place, entrance, event, session, offer, external)
		if err != nil {
			return err
		}
	}
	return nil
}
