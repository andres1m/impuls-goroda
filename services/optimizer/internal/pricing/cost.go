package pricing

import (
	"errors"
	"math"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var unknownPrice = domain.UnknownCostComponent{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}

// Cost prices the visits of a chosen route in order. A program payment is only an estimate:
// the user's share stays unknown because nobody has confirmed the benefit applies or that
// the program has enough money.
func (p Policy) Cost(visits []*domain.Candidate) ([]domain.CostSnapshot, domain.CostSummary, error) {
	if err := p.Validate(); err != nil {
		return nil, domain.CostSummary{}, err
	}
	zero := domain.Money{Currency: p.Currency}
	summary := domain.CostSummary{KnownPersonal: zero, KnownTransport: zero, ProgramAmount: zero}
	snapshots := make([]domain.CostSnapshot, len(visits))
	var lower, upper int64
	allKnown := true
	for i, c := range visits {
		q := p.priced(c)
		s := domain.CostSnapshot{Price: q.Price, Provenance: provenance(c, q.Offer)}
		if q.Offer != nil {
			id := q.Offer.ID
			s.PriceOfferID = &id
			s.Audience = q.Offer.Audience
		}
		bound, known := q.Price.UpperBound()
		top := bound.AmountMinor
		switch {
		case !known:
			allKnown = false
			s.UnknownComponents = []domain.UnknownCostComponent{unknownPrice}
		default:
			// Every other sum is bounded by the upper total, so checking it alone is enough.
			if top > math.MaxInt64-upper {
				return nil, domain.CostSummary{}, errors.New("route cost overflows")
			}
			lower += *q.Price.LowerMinor
			upper += top
			if q.Program != "" && top > 0 {
				s.ProgramAmount = &domain.Money{AmountMinor: top, Currency: p.Currency}
				summary.ProgramAmount.AmountMinor += top
			} else if q.Price.Status != domain.PriceRange {
				s.PersonalAmount = &domain.Money{AmountMinor: top, Currency: p.Currency}
				summary.KnownPersonal.AmountMinor += top
			}
		}
		snapshots[i] = s
	}
	if allKnown {
		summary.TotalLower = &domain.Money{AmountMinor: lower, Currency: p.Currency}
		summary.TotalUpper = &domain.Money{AmountMinor: upper, Currency: p.Currency}
	} else {
		summary.UnknownComponents = []domain.UnknownCostComponent{unknownPrice}
	}
	summary.BudgetConclusion = p.conclude(allKnown, upper)
	return snapshots, summary, nil
}

// priced still prices a visit the constraints would exclude, so an older plan gets an honest cost.
func (p Policy) priced(c *domain.Candidate) Quote {
	if q, ok := p.Quote(c); ok {
		return q
	}
	relaxed := p
	relaxed.PushkinCardOnly = false
	relaxed.AcceptUnknownPrice = true
	q, _ := relaxed.Quote(c)
	return q
}

func (p Policy) conclude(allKnown bool, upper int64) domain.BudgetConclusion {
	switch {
	case p.Budget.Mode == domain.BudgetNone:
		return domain.BudgetNotApplicable
	case !allKnown:
		return domain.BudgetUnknown
	case upper <= p.Budget.Limit.AmountMinor:
		return domain.BudgetSatisfied
	default:
		return domain.BudgetViolated
	}
}

func provenance(c *domain.Candidate, offer *domain.PriceOffer) domain.Provenance {
	switch {
	case offer != nil:
		return offer.Provenance
	case c.Session != nil:
		return c.Session.Provenance
	default:
		return c.Place.Provenance
	}
}
