// Package pricing decides what a visit costs the user and whether the route's money
// constraints allow it. The budget covers ticket prices only; travel is not priced.
package pricing

import (
	"bytes"
	"errors"
	"slices"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Policy holds the money constraints of one route.
type Policy struct {
	Currency string
	Budget   domain.Budget
	// Programs the user can pay with.
	Programs []string
	// Audiences the user claims, such as student.
	Audiences          []string
	PushkinCardOnly    bool
	AcceptUnknownPrice bool
}

func PolicyFor(currency string, c *domain.RouteConstraints) Policy {
	return Policy{
		Currency:           currency,
		Budget:             c.Budget,
		Programs:           c.BenefitPrograms,
		Audiences:          c.AudienceClaims,
		PushkinCardOnly:    c.PushkinCardOnly,
		AcceptUnknownPrice: slices.Contains(c.AcceptedUnknowns, domain.AcceptUnknownPrice),
	}
}

func (p *Policy) Validate() error {
	if err := (domain.Money{Currency: p.Currency}).Validate(); err != nil {
		return err
	}
	if err := p.Budget.Validate(); err != nil {
		return err
	}
	if p.Budget.Limit != nil && p.Budget.Limit.Currency != p.Currency {
		return errors.New("budget currency differs from the route currency")
	}
	return nil
}

// Quote is the price of a visit for this user.
type Quote struct {
	// Nil when no offer applies to the user.
	Offer *domain.PriceOffer
	Price domain.Price
	// The user's program the offer accepts; empty when none does.
	Program string
}

// Quote prices the visit; false means the route's constraints exclude it.
func (p *Policy) Quote(c *domain.Candidate) (Quote, bool) {
	offers := p.applicable(c)
	if p.PushkinCardOnly {
		covered := slices.DeleteFunc(slices.Clone(offers), func(o *domain.PriceOffer) bool { return !coveredByCard(o) })
		if len(offers) > 0 && len(covered) == 0 {
			return Quote{}, false
		}
		offers = covered
	}
	q := Quote{Price: domain.Price{Status: domain.PriceUnknown, Currency: p.Currency}}
	if len(offers) > 0 {
		q.Offer = slices.MinFunc(offers, compareOffers)
		q.Price = q.Offer.Price
		q.Program = p.program(q.Offer)
	}
	if _, known := q.Price.UpperBound(); !known && !p.AcceptUnknownPrice &&
		(p.Budget.Mode == domain.BudgetStrict || p.PushkinCardOnly) {
		return Quote{}, false
	}
	return q, true
}

// applicable keeps the offers the user can surely buy. The user's age is never known,
// so an age-limited tariff cannot prove the price.
func (p *Policy) applicable(c *domain.Candidate) []*domain.PriceOffer {
	var offers []*domain.PriceOffer
	for i := range c.Offers {
		o := &c.Offers[i]
		if o.Audience != domain.AudienceGeneral && !slices.Contains(p.Audiences, string(o.Audience)) {
			continue
		}
		if o.Price.Currency != p.Currency || o.EligibilityAgeMin != nil || o.EligibilityAgeMax != nil {
			continue
		}
		if o.ValidUntil != nil && o.ValidUntil.Before(c.Window.Start) {
			continue
		}
		offers = append(offers, o)
	}
	return offers
}

// coveredByCard keeps an unknown price, which the user may still accept explicitly.
func coveredByCard(o *domain.PriceOffer) bool {
	if slices.Contains(o.BenefitPrograms, domain.ProgramPushkinCard) || o.Price.Status == domain.PriceUnknown {
		return true
	}
	return o.Price.UpperMinor != nil && *o.Price.UpperMinor == 0
}

func compareOffers(a, b *domain.PriceOffer) int {
	ua, knownA := a.Price.UpperBound()
	ub, knownB := b.Price.UpperBound()
	switch {
	case knownA && !knownB:
		return -1
	case !knownA && knownB:
		return 1
	case knownA && ua.AmountMinor != ub.AmountMinor:
		if ua.AmountMinor < ub.AmountMinor {
			return -1
		}
		return 1
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

func (p *Policy) program(o *domain.PriceOffer) string {
	for _, program := range p.Programs {
		if slices.Contains(o.BenefitPrograms, program) {
			return program
		}
	}
	return ""
}

// Fits reports whether a strict budget still holds after spent plus this visit's upper price.
func (p *Policy) Fits(spent domain.Money, q Quote) bool {
	upper, known := q.Price.UpperBound()
	if p.Budget.Mode != domain.BudgetStrict || !known {
		return true
	}
	return upper.AmountMinor <= p.Budget.Limit.AmountMinor-spent.AmountMinor
}
