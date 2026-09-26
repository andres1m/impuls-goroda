package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

type Money struct {
	AmountMinor int64
	Currency    string
}

func (m Money) Validate() error {
	if m.AmountMinor < 0 {
		return errors.New("money amount must not be negative")
	}
	if strings.TrimSpace(m.Currency) == "" {
		return errors.New("money currency is required")
	}
	return nil
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
	if strings.TrimSpace(p.Currency) == "" {
		return errors.New("price currency is required")
	}
	switch p.Status {
	case PriceFree:
		if p.LowerMinor == nil || p.UpperMinor == nil || *p.LowerMinor != 0 || *p.UpperMinor != 0 {
			return errors.New("free price must have zero bounds")
		}
	case PriceFixed:
		if p.LowerMinor == nil || p.UpperMinor == nil || *p.LowerMinor < 0 || *p.LowerMinor != *p.UpperMinor {
			return errors.New("fixed price must have equal non-negative bounds")
		}
	case PriceRange:
		if p.LowerMinor == nil || p.UpperMinor == nil || *p.LowerMinor < 0 || *p.UpperMinor < *p.LowerMinor {
			return errors.New("price range bounds are invalid")
		}
	case PriceUnknown:
		if p.LowerMinor != nil || p.UpperMinor != nil {
			return errors.New("unknown price must not have bounds")
		}
	default:
		return errors.New("invalid price status")
	}
	return nil
}

type Coordinate struct {
	Longitude float64
	Latitude  float64
}

func (c Coordinate) Validate() error {
	if math.IsNaN(c.Longitude) || math.IsInf(c.Longitude, 0) || math.IsNaN(c.Latitude) || math.IsInf(c.Latitude, 0) || c.Longitude < -180 || c.Longitude > 180 || c.Latitude < -90 || c.Latitude > 90 {
		return errors.New("coordinate is outside valid range")
	}
	return nil
}

type DataMode string

const (
	DataLive      DataMode = "live"
	DataPrepared  DataMode = "prepared"
	DataSynthetic DataMode = "synthetic"
)

func (m DataMode) Validate() error {
	if m != DataLive && m != DataPrepared && m != DataSynthetic {
		return errors.New("invalid data mode")
	}
	return nil
}

type FactProvenance struct {
	SourceName      string
	SourceURL       *string
	SourceRecordID  *SourceRecordID
	SourceUpdatedAt *time.Time
	FetchedAt       time.Time
	VerifiedAt      *time.Time
}

func (p FactProvenance) Validate() error {
	if strings.TrimSpace(p.SourceName) == "" || p.FetchedAt.IsZero() {
		return errors.New("provenance source and fetched time are required")
	}
	if p.SourceRecordID != nil {
		if err := requiredID([16]byte(*p.SourceRecordID)); err != nil {
			return err
		}
	}
	if p.SourceUpdatedAt != nil && p.SourceUpdatedAt.IsZero() {
		return errors.New("source update time is invalid")
	}
	if p.VerifiedAt != nil && p.VerifiedAt.IsZero() {
		return errors.New("verification time is invalid")
	}
	return nil
}

type UnknownCostComponent struct {
	Code    string
	Message string
}

func (c UnknownCostComponent) Validate() error {
	if strings.TrimSpace(c.Code) == "" || strings.TrimSpace(c.Message) == "" {
		return errors.New("unknown cost component code and message are required")
	}
	return nil
}

type CostSnapshot struct {
	PriceOfferID      *PriceOfferID
	Audience          string
	Price             Price
	PersonalAmount    *Money
	ProgramAmount     *Money
	UnknownComponents []UnknownCostComponent
	Provenance        FactProvenance
}

func (s CostSnapshot) Validate() error {
	if err := s.Price.Validate(); err != nil {
		return err
	}
	if s.PriceOfferID != nil {
		if err := requiredID([16]byte(*s.PriceOfferID)); err != nil {
			return err
		}
	}
	for _, amount := range []*Money{s.PersonalAmount, s.ProgramAmount} {
		if amount != nil {
			if err := amount.Validate(); err != nil {
				return err
			}
			if amount.Currency != s.Price.Currency {
				return errors.New("cost snapshot currencies must match")
			}
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
	KnownPersonal     Money
	KnownTransport    Money
	ProgramAmount     Money
	TotalLower        *Money
	TotalUpper        *Money
	UnknownComponents []UnknownCostComponent
	BudgetConclusion  BudgetConclusion
}

func (s CostSummary) Validate() error {
	amounts := []Money{s.KnownPersonal, s.KnownTransport, s.ProgramAmount}
	for _, amount := range amounts {
		if err := amount.Validate(); err != nil {
			return err
		}
		if amount.Currency != s.KnownPersonal.Currency {
			return errors.New("route cost currencies must match")
		}
	}
	if s.KnownTransport.AmountMinor > s.KnownPersonal.AmountMinor {
		return errors.New("transport cost must be included in personal cost")
	}
	if (s.TotalLower == nil) != (s.TotalUpper == nil) {
		return errors.New("total cost bounds must be provided together")
	}
	if s.TotalLower != nil {
		if err := s.TotalLower.Validate(); err != nil {
			return err
		}
		if err := s.TotalUpper.Validate(); err != nil {
			return err
		}
		if s.TotalLower.Currency != s.KnownPersonal.Currency || s.TotalUpper.Currency != s.KnownPersonal.Currency || s.TotalUpper.AmountMinor < s.TotalLower.AmountMinor {
			return errors.New("total cost range is invalid")
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
