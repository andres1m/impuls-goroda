package domain

import "errors"

type Money struct {
	AmountMinor int64
	Currency    string
}

func (m Money) Validate() error {
	if m.AmountMinor < 0 {
		return errors.New("money amount must not be negative")
	}
	if !validCurrency(m.Currency) {
		return errors.New("money currency must be an ISO 4217 code")
	}
	return nil
}

func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

type PriceStatus string

const (
	PriceFree    PriceStatus = "free"
	PriceFixed   PriceStatus = "fixed"
	PriceRange   PriceStatus = "range"
	PriceUnknown PriceStatus = "unknown"
)

type Price struct {
	Status     PriceStatus
	Currency   string
	LowerMinor *int64
	UpperMinor *int64
}

func (p Price) Validate() error {
	if !validCurrency(p.Currency) {
		return errors.New("price currency must be an ISO 4217 code")
	}
	lower, upper := p.LowerMinor, p.UpperMinor
	switch p.Status {
	case PriceFree:
		if lower == nil || upper == nil || *lower != 0 || *upper != 0 {
			return errors.New("free price must have zero bounds")
		}
	case PriceFixed:
		if lower == nil || upper == nil || *lower < 0 || *lower != *upper {
			return errors.New("fixed price must have equal non-negative bounds")
		}
	case PriceRange:
		if lower == nil || upper == nil || *lower < 0 || *upper < *lower {
			return errors.New("price range bounds are invalid")
		}
	case PriceUnknown:
		if lower != nil || upper != nil {
			return errors.New("unknown price must not have bounds")
		}
	default:
		return errors.New("invalid price status")
	}
	return nil
}

// UpperBound is the amount a strict budget must be checked against; an unknown price has none.
func (p Price) UpperBound() (Money, bool) {
	if p.Status == PriceUnknown || p.UpperMinor == nil {
		return Money{}, false
	}
	return Money{AmountMinor: *p.UpperMinor, Currency: p.Currency}, true
}
