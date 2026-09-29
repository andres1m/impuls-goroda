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

const currencyCodeLen = 3

func validCurrency(code string) bool {
	if len(code) != currencyCodeLen {
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
	Status     PriceStatus `json:"Status"`
	Currency   string      `json:"Currency"`
	LowerMinor *int64      `json:"LowerMinor"`
	UpperMinor *int64      `json:"UpperMinor"`
}

func (p Price) Validate() error {
	if !validCurrency(p.Currency) {
		return errors.New("price currency must be an ISO 4217 code")
	}
	switch p.Status {
	case PriceFree:
		return validateFreePrice(p.LowerMinor, p.UpperMinor)
	case PriceFixed:
		return validateFixedPrice(p.LowerMinor, p.UpperMinor)
	case PriceRange:
		return validateRangePrice(p.LowerMinor, p.UpperMinor)
	case PriceUnknown:
		if p.LowerMinor != nil || p.UpperMinor != nil {
			return errors.New("unknown price must not have bounds")
		}
		return nil
	default:
		return errors.New("invalid price status")
	}
}

func validateFreePrice(lower, upper *int64) error {
	if lower == nil || upper == nil || *lower != 0 || *upper != 0 {
		return errors.New("free price must have zero bounds")
	}
	return nil
}

func validateFixedPrice(lower, upper *int64) error {
	if lower == nil || upper == nil || *lower < 0 || *lower != *upper {
		return errors.New("fixed price must have equal non-negative bounds")
	}
	return nil
}

func validateRangePrice(lower, upper *int64) error {
	if lower == nil || upper == nil || *lower < 0 || *upper < *lower {
		return errors.New("price range bounds are invalid")
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
