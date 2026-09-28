package usecase

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var genCategories = []domain.Category{
	domain.CategoryCulture, domain.CategorySport, domain.CategoryVolunteer,
	domain.CategoryWalk, domain.CategoryTourism, domain.CategoryGastro,
}

const genInterests = 1<<(domain.InterestCinema+1) - 1

type genCase struct {
	req      domain.OptimizeRequest
	pool     []domain.Candidate
	provider *fakeProvider
}

// genID keeps generated IDs apart from the hand-written fixtures, which use one leading byte.
func genID(i int) [16]byte {
	return [16]byte{0xA0, byte(i >> 8), byte(i)}
}

func minutes(n int) time.Duration {
	return time.Duration(n) * time.Minute
}

func genLocation(r *rand.Rand, spread float64) domain.Coordinate {
	return domain.Coordinate{
		Longitude: origin.Longitude + (r.Float64()-0.5)*0.1*spread,
		Latitude:  origin.Latitude + (r.Float64()-0.5)*0.05*spread,
	}
}

func genPlace(r *rand.Rand, i int) domain.Candidate {
	category := genCategories[r.IntN(len(genCategories))]
	open := at(7+r.IntN(5), 0)
	closes := open.Add(minutes(120 + r.IntN(12)*60))
	minDuration := minutes(20 + r.IntN(4)*10)
	c := domain.Candidate{
		Place: domain.Place{
			ID: genID(i), City: "perm", Title: "Place", Category: &category,
			InterestMask: domain.InterestMask(r.Uint64() & genInterests), Location: genLocation(r, 1),
			DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window: domain.VisitWindow{
			Kind: domain.WindowContinuous, Start: open, End: closes,
			MinDuration: minDuration, RecommendedDuration: minDuration + minutes(r.IntN(4)*15),
			ArrivalBuffer: minutes(r.IntN(3) * 5),
		},
		BaseScore: 1,
	}
	if r.IntN(4) == 0 {
		c.Window.LastEntryAt = ptr(closes.Add(-minDuration - minutes(r.IntN(3)*15)))
	}
	return c
}

func genPrice(r *rand.Rand) domain.Price {
	amount := int64(r.IntN(20)) * 10000
	switch r.IntN(5) {
	case 0:
		return domain.Price{Status: domain.PriceFree, Currency: "RUB", LowerMinor: ptr(int64(0)), UpperMinor: ptr(int64(0))}
	case 1:
		return domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}
	case 2:
		return domain.Price{Status: domain.PriceRange, Currency: "RUB", LowerMinor: ptr(amount), UpperMinor: ptr(amount + 50000)}
	default:
		return domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: ptr(amount), UpperMinor: ptr(amount)}
	}
}

func genSession(r *rand.Rand, i int) domain.Candidate {
	c := genPlace(r, i)
	start := at(9+r.IntN(9), r.IntN(4)*15)
	end := start.Add(minutes(45 + r.IntN(6)*15))
	window := domain.VisitWindow{
		Kind: domain.WindowFixed, Start: start, End: end,
		MinDuration: end.Sub(start), RecommendedDuration: end.Sub(start), ArrivalBuffer: minutes(r.IntN(3) * 5),
	}
	c.Event = &domain.Event{
		ID: domain.EventID(genID(i)), PlaceID: c.Place.ID, Title: "Event", Category: *c.Place.Category,
		InterestMask: c.Place.InterestMask, DataMode: domain.DataSynthetic, Provenance: source,
	}
	availability := domain.AvailabilityAvailable
	switch r.IntN(10) {
	case 0:
		availability = domain.AvailabilityCancelled
	case 1:
		availability = domain.AvailabilitySoldOut
	}
	c.Session = &domain.Session{
		ID: domain.SessionID(genID(i)), EventID: c.Event.ID, Window: window, Access: domain.AccessTicket,
		Availability: availability, Version: 1, DataMode: domain.DataSynthetic, Provenance: source,
	}
	c.Window = window
	var programs []string
	if r.IntN(3) == 0 {
		programs = []string{domain.ProgramPushkinCard}
	}
	c.Offers = []domain.PriceOffer{{
		ID: domain.PriceOfferID(genID(i)), SessionID: c.Session.ID, Audience: domain.AudienceGeneral,
		Price: genPrice(r), BenefitPrograms: programs, Provenance: source,
	}}
	return c
}

func genPool(r *rand.Rand, n int) []domain.Candidate {
	pool := make([]domain.Candidate, n)
	for i := range pool {
		if r.IntN(3) == 0 {
			pool[i] = genSession(r, i+1)
		} else {
			pool[i] = genPlace(r, i+1)
		}
	}
	return pool
}

var genModes = [][]domain.MovementMode{
	{domain.MovementWalk},
	{domain.MovementWalk, domain.MovementTransit},
	{domain.MovementWalk, domain.MovementTransit, domain.MovementCar},
}

func genBudget(r *rand.Rand) domain.Budget {
	limit := &domain.Money{AmountMinor: int64(r.IntN(10)) * 50000, Currency: "RUB"}
	switch r.IntN(3) {
	case 1:
		return domain.Budget{Mode: domain.BudgetAdvisory, Limit: limit}
	case 2:
		return domain.Budget{Mode: domain.BudgetStrict, Limit: limit}
	default:
		return domain.Budget{Mode: domain.BudgetNone}
	}
}

// generate builds one planning problem from a seed, so a failing seed replays exactly.
func generate(seed uint64) genCase {
	r := rand.New(rand.NewPCG(seed, 0))
	pool := genPool(r, 5+r.IntN(36))
	start := at(8+r.IntN(5), r.IntN(4)*15)
	req := domain.OptimizeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Start: start, End: start.Add(minutes(180 + r.IntN(8)*60)),
		Origin: origin,
		Constraints: domain.RouteConstraints{
			MovementModes: genModes[r.IntN(len(genModes))],
			LoadProfile:   "moderate",
			Budget:        genBudget(r),
		},
	}
	if r.IntN(2) == 0 {
		req.Constraints.InterestMask = domain.InterestMask(r.Uint64() & genInterests)
	}
	if r.IntN(2) == 0 {
		req.Destination = ptr(genLocation(r, 0.5))
	}
	if r.IntN(2) == 0 {
		req.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
	}
	if r.IntN(4) == 0 {
		req.Constraints.ExcludedCategories = []domain.Category{genCategories[r.IntN(len(genCategories))]}
	}
	if r.IntN(5) == 0 {
		req.Constraints.PushkinCardOnly = true
		req.Constraints.BenefitPrograms = []string{domain.ProgramPushkinCard}
	}
	if r.IntN(4) == 0 {
		lunch := req.Start.Add(minutes(60 + r.IntN(3)*30))
		req.Constraints.LunchWindow = &domain.LunchWindow{Start: lunch, End: lunch.Add(minutes(90)), MinDuration: minutes(45)}
	}
	for _, c := range pool {
		if c.Session != nil && c.Session.Availability == domain.AvailabilityAvailable && r.IntN(6) == 0 && len(req.Constraints.Obligations) < 2 {
			req.Constraints.Obligations = append(req.Constraints.Obligations, domain.Obligation{SessionID: &c.Session.ID, Participation: domain.ParticipationUserReported})
		}
	}
	provider := estimated()
	if r.IntN(5) == 0 {
		provider = &fakeProvider{transit: baseline(), degraded: true}
	}
	// Drawn after the rest of the problem, so the pace never changes what a seed generates before it.
	req.Constraints.LoadProfile = []string{"relaxed", "moderate", "intense"}[r.IntN(3)]
	// A reported ticket holds a place even once the session sells out. Drawn after the pace for the same
	// reason, and only a session the day can reach, so the ticket itself does not make the day infeasible.
	for _, c := range pool {
		reachable := c.Session != nil && !c.Window.Start.Before(req.Start.Add(time.Hour)) && !c.Window.End.After(req.End)
		if reachable && c.Session.Availability == domain.AvailabilitySoldOut && len(req.Constraints.Obligations) == 0 && r.IntN(2) == 0 {
			req.Constraints.Obligations = append(req.Constraints.Obligations, domain.Obligation{SessionID: &c.Session.ID, Participation: domain.ParticipationUserReported})
		}
	}
	// Some sources let people in after the start: half the session is enough, sometimes only until a
	// last entry. Drawn last for the same reason.
	for i := range pool {
		if pool[i].Session == nil || r.IntN(4) != 0 {
			continue
		}
		late := true
		w := pool[i].Window
		w.LateEntryAllowed, w.MinDuration = &late, w.End.Sub(w.Start)/2
		if r.IntN(2) == 0 {
			lastEntry := w.Start.Add(w.MinDuration / 2)
			w.LastEntryAt = &lastEntry
		}
		session := *pool[i].Session
		session.Window = w
		pool[i].Session, pool[i].Window = &session, w
	}
	return genCase{req: req, pool: pool, provider: provider}
}

func TestGeneratedCasesAreValidAndVaried(t *testing.T) {
	var strict, unknownPrice, obligations, soldOutObligations, degraded, lateEntry int
	for seed := range uint64(200) {
		c := generate(seed)
		if err := c.req.Validate(); err != nil {
			t.Fatalf("seed %d: request: %v", seed, err)
		}
		for i, cand := range c.pool {
			if err := cand.Validate(); err != nil {
				t.Fatalf("seed %d: candidate %d: %v", seed, i, err)
			}
		}
		if c.req.Constraints.Budget.Mode == domain.BudgetStrict {
			strict++
		}
		if slices.ContainsFunc(c.pool, func(cand domain.Candidate) bool {
			return slices.ContainsFunc(cand.Offers, func(o domain.PriceOffer) bool { return o.Price.Status == domain.PriceUnknown })
		}) {
			unknownPrice++
		}
		if len(c.req.Constraints.Obligations) > 0 {
			obligations++
		}
		if slices.ContainsFunc(c.req.Constraints.Obligations, func(o domain.Obligation) bool {
			return slices.ContainsFunc(c.pool, func(cand domain.Candidate) bool {
				return cand.Session != nil && cand.Session.ID == *o.SessionID && cand.Session.Availability == domain.AvailabilitySoldOut
			})
		}) {
			soldOutObligations++
		}
		if c.provider.degraded {
			degraded++
		}
		if slices.ContainsFunc(c.pool, func(cand domain.Candidate) bool {
			return cand.Window.LateEntryAllowed != nil && cand.Window.LastEntryAt != nil
		}) {
			lateEntry++
		}
	}
	paces := map[string]int{}
	for seed := range uint64(200) {
		paces[generate(seed).req.Constraints.LoadProfile]++
	}
	for name, n := range map[string]int{"strict": strict, "unknown price": unknownPrice, "obligations": obligations, "degraded": degraded, "late entry until a last entry": lateEntry,
		"relaxed pace": paces["relaxed"], "moderate pace": paces["moderate"], "intense pace": paces["intense"]} {
		if n < 20 {
			t.Errorf("only %d of 200 seeds have %s", n, name)
		}
	}
	if soldOutObligations < 10 {
		t.Errorf("only %d of 200 seeds hold a place in a sold-out session", soldOutObligations)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, b := generate(42), generate(42)
	if !reflect.DeepEqual(a.req, b.req) || !reflect.DeepEqual(a.pool, b.pool) {
		t.Fatal("same seed built different cases")
	}
}
