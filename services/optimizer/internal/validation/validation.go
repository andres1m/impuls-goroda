// Package validation checks a finished plan against the hard rules with its own code, never the
// search's, so a mistake in the search cannot certify its own result.
package validation

import (
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Input is what the plan was built from.
type Input struct {
	Constraints domain.RouteConstraints
	Currency    string
	// The catalog candidate behind each visit step, with its original window and offers.
	Candidates map[domain.VisitID]domain.Candidate
	// Routing fell back to straight lines, so no leg may claim to be more than unknown.
	Degraded bool
	// Visits already done: their actual times and the travel to them are history, not plan.
	History map[domain.VisitID]struct{}
}

// Violation is a broken rule; a plan with any is not a result.
type Violation struct {
	Code    string
	VisitID *domain.VisitID
	Message string
}

func Check(plan domain.Plan, in Input) []Violation {
	c := &checker{plan: plan, in: in}
	if err := in.Constraints.Budget.Validate(); err != nil {
		c.add("INPUT_INVALID", nil, err.Error())
		return c.violations
	}
	if err := plan.Validate(); err != nil {
		c.add("PLAN_MALFORMED", nil, err.Error())
		return c.violations
	}
	c.visits()
	c.legs()
	c.obligations()
	c.cost()
	return c.violations
}

type checker struct {
	plan       domain.Plan
	in         Input
	violations []Violation
}

func (c *checker) add(code string, visit *domain.VisitID, message string) {
	c.violations = append(c.violations, Violation{Code: code, VisitID: visit, Message: message})
}

func matchesObligation(o domain.Obligation, step domain.Step) bool {
	return (o.VisitID != nil && *o.VisitID == step.VisitID) ||
		(o.SessionID != nil && step.Catalog != nil && step.Catalog.SessionID != nil && *o.SessionID == *step.Catalog.SessionID)
}

func (c *checker) obligation(step domain.Step) (domain.Obligation, bool) {
	i := slices.IndexFunc(c.in.Constraints.Obligations, func(o domain.Obligation) bool { return matchesObligation(o, step) })
	if i < 0 {
		return domain.Obligation{}, false
	}
	return c.in.Constraints.Obligations[i], true
}

func (c *checker) visits() {
	places := make(map[domain.PlaceID]struct{}, len(c.plan.Steps))
	for i, step := range c.plan.Steps {
		if step.Kind != domain.StepVisit {
			continue
		}
		id := &c.plan.Steps[i].VisitID
		if _, done := c.in.History[step.VisitID]; done {
			places[step.Catalog.PlaceID] = struct{}{}
			continue
		}
		cand, ok := c.in.Candidates[step.VisitID]
		if !ok {
			c.add("STEP_WITHOUT_CANDIDATE", id, "The visit has no catalog candidate behind it")
			continue
		}
		if step.Catalog.PlaceID != cand.Place.ID || !sameSession(step.Catalog.SessionID, cand.Session) {
			c.add("STEP_CATALOG_MISMATCH", id, "The visit shows a different place or session than it was planned for")
		}
		if _, repeated := places[cand.Place.ID]; repeated {
			c.add("PLACE_REPEATED", id, "The route visits the same place twice")
		}
		places[cand.Place.ID] = struct{}{}
		obligation, pinned := c.obligation(step)
		// A session the user committed to outranks a category the user excluded.
		if !pinned && slices.Contains(c.in.Constraints.ExcludedCategories, cand.Category()) {
			c.add("CATEGORY_EXCLUDED", id, "The visit belongs to a category the user excluded")
		}
		if cand.Session != nil {
			switch cand.Session.Availability {
			case domain.AvailabilityCancelled:
				c.add("SESSION_CANCELLED", id, "The session is cancelled")
			case domain.AvailabilitySoldOut:
				if !pinned || !holdsPlace(obligation.Participation) {
					c.add("SESSION_SOLD_OUT", id, "The session is sold out and the user holds no place")
				}
			}
		}
		buffer := cand.Window.ArrivalBuffer
		if pinned {
			buffer = max(buffer, obligation.ArrivalBuffer)
		}
		// The step's arrival is what the user sees; it is never earlier than the leg's.
		c.timing(id, step, cand.Window, step.ArrivalAt, buffer)
	}
}

func (c *checker) timing(id *domain.VisitID, step domain.Step, w domain.VisitWindow, arrival time.Time, buffer time.Duration) {
	start, end := step.VisitStartAt, step.VisitEndAt
	switch w.Kind {
	case domain.WindowFixed:
		lateEntry := w.LateEntryAllowed != nil && *w.LateEntryAllowed
		switch {
		case !end.Equal(w.End) || start.Before(w.Start) || (start.After(w.Start) && !lateEntry):
			c.add("FIXED_SESSION_MOVED", id, "The visit does not keep the session's times")
		case start.Equal(w.Start) && !lateEntry:
			if start.Sub(arrival) < buffer {
				c.add("ARRIVAL_BUFFER_MISSED", id, "The user arrives without the required time before the session")
			}
		default:
			lastEntry := w.End.Add(-w.MinDuration)
			if w.LastEntryAt != nil {
				lastEntry = *w.LastEntryAt
			}
			if start.After(lastEntry) {
				c.add("LAST_ENTRY_MISSED", id, "The user enters after the last entry")
			}
		}
	case domain.WindowContinuous:
		if start.Before(w.Start) || end.After(w.End) {
			c.add("VISIT_OUTSIDE_WINDOW", id, "The visit does not fit into the opening hours")
		}
		if w.LastEntryAt != nil && start.After(*w.LastEntryAt) {
			c.add("LAST_ENTRY_MISSED", id, "The user enters after the last entry")
		}
		if start.Sub(arrival) < buffer {
			c.add("ARRIVAL_BUFFER_MISSED", id, "The user arrives without the required time before the visit")
		}
	}
	if end.Sub(start) < w.MinDuration {
		c.add("VISIT_TOO_SHORT", id, "The visit is shorter than its minimum duration")
	}
}

func (c *checker) legs() {
	for _, leg := range c.plan.Legs {
		if leg.ToVisitID != nil {
			if _, done := c.in.History[*leg.ToVisitID]; done {
				continue
			}
		}
		if !slices.Contains(c.in.Constraints.MovementModes, leg.Mode) {
			c.add("LEG_MODE_NOT_ALLOWED", leg.ToVisitID, "The user did not allow this way of travelling")
		}
		// No source confirms a whole path yet, and a straight-line fallback cannot even rule out a river.
		if leg.Verification == domain.VerificationVerified || (c.in.Degraded && leg.Verification != domain.VerificationUnknown) {
			c.add("LEG_STATUS_OVERSTATED", leg.ToVisitID, "The travel is marked as more certain than its source allows")
		}
	}
}

func (c *checker) obligations() {
	for i, step := range c.plan.Steps {
		if step.Obligation && step.Catalog != nil {
			if _, ok := c.obligation(step); !ok {
				c.add("OBLIGATION_UNEXPECTED", &c.plan.Steps[i].VisitID, "The visit is marked as an obligation the user never made")
			}
		}
	}
	seen := make(map[domain.SessionID]struct{}, len(c.in.Constraints.Obligations))
	for _, o := range c.in.Constraints.Obligations {
		// A repeated obligation adds nothing; the first one for a session is the one that counts.
		if o.SessionID != nil {
			if _, repeated := seen[*o.SessionID]; repeated {
				continue
			}
			seen[*o.SessionID] = struct{}{}
		}
		i := slices.IndexFunc(c.plan.Steps, func(s domain.Step) bool {
			return matchesObligation(o, s)
		})
		if i < 0 {
			c.add("OBLIGATION_MISSING", nil, "A session the user committed to is not in the route")
			continue
		}
		step := c.plan.Steps[i]
		if !step.Obligation {
			c.add("OBLIGATION_NOT_MARKED", &c.plan.Steps[i].VisitID, "The committed session is not marked as an obligation")
		}
		if step.Participation.Status != o.Participation {
			c.add("OBLIGATION_PARTICIPATION_CHANGED", &c.plan.Steps[i].VisitID, "The participation of the committed session differs from what the user reported")
		}
	}
}

func (c *checker) cost() {
	cons := c.in.Constraints
	strict := cons.Budget.Mode == domain.BudgetStrict
	acceptedUnknown := slices.Contains(cons.AcceptedUnknowns, domain.AcceptUnknownPrice)
	var lower, upper, personal, program int64
	unknown := false
	for i, step := range c.plan.Steps {
		if step.Kind != domain.StepVisit {
			continue
		}
		id := &c.plan.Steps[i].VisitID
		snapshot := step.Cost
		// What was paid for a visit already done is history: it counts, but is not judged again.
		_, done := c.in.History[step.VisitID]
		var offer *domain.PriceOffer
		if !done {
			cand, ok := c.in.Candidates[step.VisitID]
			if !ok {
				continue
			}
			offer = c.offer(id, cand, snapshot)
		}
		if snapshot.PersonalAmount != nil {
			personal += snapshot.PersonalAmount.AmountMinor
		}
		top, known := snapshot.Price.UpperBound()
		if snapshot.ProgramAmount != nil {
			program += snapshot.ProgramAmount.AmountMinor
		}
		if !known {
			unknown = true
		} else {
			lower += *snapshot.Price.LowerMinor
			upper += top.AmountMinor
		}
		if done {
			continue
		}
		if snapshot.ProgramAmount != nil {
			if snapshot.PersonalAmount != nil {
				c.add("PROGRAM_SHARE_PROMISED", id, "The user's share is stated although a program may pay and nobody confirmed it")
			}
			if !known || snapshot.ProgramAmount.AmountMinor != top.AmountMinor || offer == nil ||
				!slices.ContainsFunc(offer.BenefitPrograms, func(p string) bool { return slices.Contains(cons.BenefitPrograms, p) }) {
				c.add("PROGRAM_SHARE_INVENTED", id, "A program payment is estimated for a tariff that does not take the user's program, or for a different amount")
			}
		}
		if snapshot.PersonalAmount != nil && (!known || snapshot.Price.Status == domain.PriceRange || snapshot.PersonalAmount.AmountMinor != top.AmountMinor) {
			c.add("PERSONAL_SHARE_MISMATCH", id, "The user's share differs from the known price")
		}
		if !known {
			if (strict || cons.PushkinCardOnly) && !acceptedUnknown {
				c.add("UNKNOWN_PRICE_NOT_ACCEPTED", id, "The price is unknown and the user did not accept that")
			}
			continue
		}
		if cons.PushkinCardOnly && top.AmountMinor > 0 && (offer == nil || !slices.Contains(offer.BenefitPrograms, domain.ProgramPushkinCard)) {
			c.add("PUSHKIN_CARD_NOT_ACCEPTED", id, "A paid visit does not accept the Pushkin card")
		}
	}
	if strict && upper > cons.Budget.Limit.AmountMinor {
		c.add("BUDGET_EXCEEDED", nil, "The known prices exceed the strict budget")
	}
	c.summary(lower, upper, personal, program, unknown)
	partial := unknown && (strict || cons.PushkinCardOnly)
	if (c.plan.Result == domain.ResultPartial) != partial {
		c.add("STATUS_MISMATCH", nil, "The route status does not match the unknowns it relies on")
	}
}

// offer checks that a stated price comes from a tariff the user can surely use and returns it.
func (c *checker) offer(id *domain.VisitID, cand domain.Candidate, snapshot *domain.CostSnapshot) *domain.PriceOffer {
	if snapshot.PriceOfferID == nil {
		if snapshot.Price.Status != domain.PriceUnknown {
			c.add("PRICE_INVENTED", id, "A price is stated without a tariff behind it")
		}
		return nil
	}
	i := slices.IndexFunc(cand.Offers, func(o domain.PriceOffer) bool { return o.ID == *snapshot.PriceOfferID })
	if i < 0 {
		c.add("PRICE_OFFER_UNKNOWN", id, "The tariff does not belong to the visit")
		return nil
	}
	offer := &cand.Offers[i]
	claimed := offer.Audience == domain.AudienceGeneral || slices.Contains(c.in.Constraints.AudienceClaims, string(offer.Audience))
	fresh := offer.ValidUntil == nil || !offer.ValidUntil.Before(cand.Window.Start)
	ageFree := offer.EligibilityAgeMin == nil && offer.EligibilityAgeMax == nil
	if !claimed || !fresh || !ageFree || offer.Price.Currency != c.in.Currency {
		c.add("PRICE_OFFER_NOT_APPLICABLE", id, "The tariff is not one the user can surely use")
	}
	if !samePrice(offer.Price, snapshot.Price) {
		c.add("PRICE_MISMATCH", id, "The stated price differs from the tariff")
	}
	return offer
}

func (c *checker) summary(lower, upper, personal, program int64, unknown bool) {
	s := c.plan.Cost
	budget := c.in.Constraints.Budget
	var conclusion domain.BudgetConclusion
	switch {
	case budget.Mode == domain.BudgetNone:
		conclusion = domain.BudgetNotApplicable
	case unknown:
		conclusion = domain.BudgetUnknown
	case upper <= budget.Limit.AmountMinor:
		conclusion = domain.BudgetSatisfied
	default:
		conclusion = domain.BudgetViolated
	}
	totalsMatch := (s.TotalUpper == nil) == unknown &&
		(unknown || (s.TotalLower.AmountMinor == lower && s.TotalUpper.AmountMinor == upper))
	// Travel is not priced, so the summary never carries a known transport cost.
	if s.KnownPersonal.AmountMinor != personal || s.ProgramAmount.AmountMinor != program || s.KnownTransport.AmountMinor != 0 ||
		s.KnownPersonal.Currency != c.in.Currency || !totalsMatch || s.BudgetConclusion != conclusion {
		c.add("COST_SUMMARY_MISMATCH", nil, "The route cost summary does not add up from its visits")
	}
}

func samePrice(a, b domain.Price) bool {
	return a.Status == b.Status && a.Currency == b.Currency && equalBound(a.LowerMinor, b.LowerMinor) && equalBound(a.UpperMinor, b.UpperMinor)
}

func equalBound(a, b *int64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func sameSession(snapshot *domain.SessionID, session *domain.Session) bool {
	if snapshot == nil || session == nil {
		return snapshot == nil && session == nil
	}
	return *snapshot == session.ID
}

func holdsPlace(status domain.ParticipationStatus) bool {
	return status == domain.ParticipationUserReported || status == domain.ParticipationProviderConfirmed
}
