package routewire

import (
	"errors"
	"strconv"
	"strings"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
)

type RouteSummary struct {
	RouteID     string      `json:"route_id"`
	Revision    string      `json:"revision"`
	Lifecycle   string      `json:"lifecycle"`
	City        string      `json:"city"`
	Timezone    string      `json:"timezone"`
	StartAt     time.Time   `json:"start_at"`
	EndAt       time.Time   `json:"end_at"`
	Result      string      `json:"result"`
	ArchetypeID string      `json:"archetype_id"`
	Cost        CostSummary `json:"cost"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type RoutePage struct {
	Routes     []RouteSummary `json:"routes"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

func (route RouteSummary) Validate() error {
	invalid := errors.New("stored route summary is invalid")
	id, err := uuid.Parse(route.RouteID)
	if err != nil || id == uuid.Nil || id.String() != route.RouteID {
		return invalid
	}
	revision, err := strconv.ParseInt(route.Revision, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != route.Revision {
		return invalid
	}
	if (route.Lifecycle != "draft" && route.Lifecycle != "saved") || (route.Result != "READY" && route.Result != "PARTIAL") || strings.TrimSpace(route.City) == "" || strings.TrimSpace(route.Timezone) == "" || strings.TrimSpace(route.ArchetypeID) == "" || route.StartAt.IsZero() || !route.EndAt.After(route.StartAt) || route.UpdatedAt.IsZero() {
		return invalid
	}
	known, err := summaryMoney(route.Cost.KnownPersonal)
	if err != nil {
		return invalid
	}
	transport, err := summaryMoney(route.Cost.KnownTransport)
	if err != nil {
		return invalid
	}
	program, err := summaryMoney(route.Cost.ProgramAmount)
	if err != nil {
		return invalid
	}
	summary := d.CostSummary{KnownPersonal: known, KnownTransport: transport, ProgramAmount: program, BudgetConclusion: d.BudgetConclusion(route.Cost.BudgetConclusion)}
	if route.Cost.TotalLower != nil {
		amount, err := summaryMoney(*route.Cost.TotalLower)
		if err != nil {
			return invalid
		}
		summary.TotalLower = &amount
	}
	if route.Cost.TotalUpper != nil {
		amount, err := summaryMoney(*route.Cost.TotalUpper)
		if err != nil {
			return invalid
		}
		summary.TotalUpper = &amount
	}
	if route.Cost.UnknownComponents == nil {
		return invalid
	}
	for _, component := range route.Cost.UnknownComponents {
		summary.UnknownComponents = append(summary.UnknownComponents, d.UnknownCostComponent{Code: component.Code, Message: component.Message})
	}
	if summary.Validate() != nil {
		return invalid
	}
	return nil
}

func summaryMoney(value Money) (d.Money, error) {
	amount, err := strconv.ParseInt(value.AmountMinor, 10, 64)
	if err != nil || amount < 0 || strconv.FormatInt(amount, 10) != value.AmountMinor || len(value.Currency) != 3 || strings.Trim(value.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return d.Money{}, errors.New("stored money is invalid")
	}
	return d.Money{AmountMinor: amount, Currency: value.Currency}, nil
}
