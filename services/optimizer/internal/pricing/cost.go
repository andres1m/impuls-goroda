package pricing

import "github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"

var unknownPrice = domain.UnknownCostComponent{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}

// Cost prices the visits of a chosen route in order. A program payment is only an estimate:
// the user's share stays unknown because nobody has confirmed the benefit applies.
func (p Policy) Cost(visits []*domain.Candidate) ([]domain.CostSnapshot, domain.CostSummary) {
	zero := domain.Money{Currency: p.Currency}
	summary := domain.CostSummary{KnownPersonal: zero, KnownTransport: zero, ProgramAmount: zero}
	balance := newBalance(p.Balance)
	snapshots := make([]domain.CostSnapshot, len(visits))
	var lower, upper int64
	allKnown := true
	for i, c := range visits {
		q := p.priced(c)
		s := domain.CostSnapshot{Price: q.Price, Provenance: provenance(c, q.Offer)}
		if q.Offer != nil {
			s.PriceOfferID = &q.Offer.ID
			s.Audience = q.Offer.Audience
		}
		top, known := q.upper()
		switch {
		case !known:
			allKnown = false
			s.UnknownComponents = []domain.UnknownCostComponent{unknownPrice}
		default:
			lower += *q.Price.LowerMinor
			upper += top
			if charge, ok := balance.charge(q.Program, top); ok {
				s.ProgramAmount = &domain.Money{AmountMinor: charge, Currency: p.Currency}
				summary.ProgramAmount.AmountMinor += charge
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
	return snapshots, summary
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

type balance struct {
	program string
	// Nil when the user did not report a balance, so nothing caps the estimate.
	left *int64
}

func newBalance(b *domain.ProgramBalance) *balance {
	if b == nil {
		return &balance{}
	}
	left := b.Balance.AmountMinor
	return &balance{program: b.Program, left: &left}
}

// charge estimates what the program pays for a ticket; false means the user pays it all.
func (b *balance) charge(program string, price int64) (int64, bool) {
	if program == "" || price == 0 {
		return 0, false
	}
	if program != b.program || b.left == nil {
		return price, true
	}
	if *b.left == 0 {
		return 0, false
	}
	paid := min(price, *b.left)
	*b.left -= paid
	return paid, true
}
