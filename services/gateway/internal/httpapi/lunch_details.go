package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lunchprovider"
	"github.com/labstack/echo/v5"
)

func (r *LunchSearchRouter) organization(c *echo.Context) error {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return authRequired()
	}
	id, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return malformedCommandHeader()
	}
	if err := RequireOwner(c.Request().Context(), r.runtime, domain.RouteID(id), principal.UserID); err != nil {
		return err
	}
	if r.details == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "LUNCH_DETAILS_NOT_CONFIGURED", Message: "Organization details are unavailable"}
	}
	value, err := r.details.Organization(c.Request().Context(), c.Param("external_id"))
	if err != nil {
		failure := &Error{Status: http.StatusServiceUnavailable, Code: "LUNCH_DETAILS_UNAVAILABLE", Message: "Organization details are unavailable", Retryable: true}
		var provider *lunchprovider.Error
		if errors.As(err, &provider) {
			failure.Retryable = provider.Retryable
			switch provider.Kind {
			case "invalid_input":
				return malformedCommandHeader()
			case "not_found":
				failure.Status, failure.Code = http.StatusNotFound, "LUNCH_ORGANIZATION_NOT_FOUND"
			case "not_food":
				failure.Status, failure.Code = http.StatusUnprocessableEntity, "LUNCH_VENUE_NOT_FOOD"
				failure.Message = "The organization is not a verified cafe"
			case "access_denied":
				failure.Code = "LUNCH_DETAILS_ACCESS_DENIED"
			case "busy":
				failure.Code = "LUNCH_DETAILS_BUSY"
			case "rate_limited":
				failure.Code = "LUNCH_DETAILS_RATE_LIMITED"
			}
		}
		return failure
	}
	return c.JSON(http.StatusOK, struct {
		RequestID    string            `json:"request_id"`
		Organization lunchOrganization `json:"organization"`
	}{RequestID: RequestID(c), Organization: lunchOrganization{
		Provider: value.Provider, ExternalID: value.ExternalID, Title: value.Title, Address: value.Address,
		Position:   lunchPosition{Longitude: value.Position.Longitude, Latitude: value.Position.Latitude},
		ObservedAt: value.ObservedAt, PriceStatus: value.PriceStatus,
		AvailabilityStatus: value.AvailabilityStatus, HoursStatus: value.HoursStatus,
	}})
}

type lunchOrganization struct {
	Provider           string        `json:"provider"`
	ExternalID         string        `json:"external_id"`
	Title              string        `json:"title"`
	Address            string        `json:"address,omitempty"`
	Position           lunchPosition `json:"position"`
	ObservedAt         time.Time     `json:"observed_at"`
	PriceStatus        string        `json:"price_status"`
	AvailabilityStatus string        `json:"availability_status"`
	HoursStatus        string        `json:"hours_status"`
}
