package domain

import (
	"errors"
	"regexp"
	"strings"
)

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func validateCodeMessage(code, message, name string) error {
	if !codePattern.MatchString(code) {
		return errors.New(name + " code must be upper snake case")
	}
	if strings.TrimSpace(message) == "" {
		return errors.New(name + " message is required")
	}
	return nil
}

type UnknownCostComponent struct {
	Code    string
	Message string
}

func (c UnknownCostComponent) Validate() error {
	return validateCodeMessage(c.Code, c.Message, "unknown cost component")
}

type CostSnapshot struct {
	PriceOfferID *PriceOfferID
	// Empty for costs without a tariff audience, such as transport.
	Audience Audience
	Price    Price
	// Nil when the user's share cannot be determined.
	PersonalAmount    *Money
	ProgramAmount     *Money
	UnknownComponents []UnknownCostComponent
	Provenance        Provenance
}

func (s CostSnapshot) Validate() error {
	if err := optionalID(s.PriceOfferID, "price offer"); err != nil {
		return err
	}
	if s.Audience != "" {
		if err := s.Audience.Validate(); err != nil {
			return err
		}
	}
	if err := s.Price.Validate(); err != nil {
		return err
	}
	for _, amount := range []*Money{s.PersonalAmount, s.ProgramAmount} {
		if amount == nil {
			continue
		}
		if err := amount.Validate(); err != nil {
			return err
		}
		if amount.Currency != s.Price.Currency {
			return errors.New("cost snapshot amounts must use the price currency")
		}
	}
	for _, component := range s.UnknownComponents {
		if err := component.Validate(); err != nil {
			return err
		}
	}
	return s.Provenance.Validate()
}

type BudgetConclusion string

const (
	BudgetNotApplicable BudgetConclusion = "not_applicable"
	BudgetSatisfied     BudgetConclusion = "satisfied"
	BudgetViolated      BudgetConclusion = "violated"
	BudgetUnknown       BudgetConclusion = "unknown"
)

type CostSummary struct {
	// Known personal spending, transport included.
	KnownPersonal     Money
	KnownTransport    Money
	ProgramAmount     Money
	TotalLower        *Money
	TotalUpper        *Money
	UnknownComponents []UnknownCostComponent
	BudgetConclusion  BudgetConclusion
}

func (s CostSummary) Validate() error {
	currency := s.KnownPersonal.Currency
	for _, amount := range []Money{s.KnownPersonal, s.KnownTransport, s.ProgramAmount} {
		if err := amount.Validate(); err != nil {
			return err
		}
		if amount.Currency != currency {
			return errors.New("route cost must use one currency")
		}
	}
	if s.KnownTransport.AmountMinor > s.KnownPersonal.AmountMinor {
		return errors.New("transport cost must be part of personal cost")
	}
	if (s.TotalLower == nil) != (s.TotalUpper == nil) {
		return errors.New("total cost bounds must be given together")
	}
	if s.TotalLower != nil {
		for _, bound := range []*Money{s.TotalLower, s.TotalUpper} {
			if err := bound.Validate(); err != nil {
				return err
			}
			if bound.Currency != currency {
				return errors.New("route cost must use one currency")
			}
		}
		if s.TotalUpper.AmountMinor < s.TotalLower.AmountMinor {
			return errors.New("total cost range is inverted")
		}
	}
	for _, component := range s.UnknownComponents {
		if err := component.Validate(); err != nil {
			return err
		}
	}
	switch s.BudgetConclusion {
	case BudgetNotApplicable, BudgetSatisfied, BudgetViolated, BudgetUnknown:
		return nil
	default:
		return errors.New("invalid budget conclusion")
	}
}
