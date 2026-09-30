package lunchprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type Error struct {
	Kind      string
	Retryable bool
}

func (e *Error) Error() string { return "lunch organization: " + e.Kind }

type Organization struct {
	Provider           string
	ExternalID         string
	Title              string
	Address            string
	Position           d.Coordinate
	ObservedAt         time.Time
	PriceStatus        string
	AvailabilityStatus string
	HoursStatus        string
}

type Client struct {
	key   string
	http  *http.Client
	slots chan struct{}
}

func NewClient(key string) (*Client, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, &Error{Kind: "access_denied"}
	}
	return &Client{key: key, slots: make(chan struct{}, 4), http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Organization(ctx context.Context, externalID string) (Organization, error) {
	failure := func(kind string, retryable bool) (Organization, error) {
		return Organization{}, &Error{Kind: kind, Retryable: retryable}
	}
	if ctx == nil || externalID != strings.TrimSpace(externalID) || !validText(externalID, 128, false) {
		return failure("invalid_input", false)
	}
	if c == nil || c.http == nil || c.key == "" || c.slots == nil {
		return failure("access_denied", false)
	}
	if ctx.Err() != nil {
		return failure("unavailable", true)
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return failure("busy", true)
	}
	endpoint := url.URL{Scheme: "https", Host: "catalog.api.2gis.com", Path: "/3.0/items/byid"}
	endpoint.RawQuery = url.Values{"id": {externalID}, "key": {c.key}, "fields": {"items.point,items.rubrics"}, "locale": {"ru_RU"}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return failure("unavailable", true)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return failure("unavailable", true)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return failure("access_denied", false)
	case http.StatusTooManyRequests:
		return failure("rate_limited", true)
	case http.StatusOK, http.StatusNotFound:
	default:
		return failure("unavailable", true)
	}
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit || !utf8.Valid(raw) {
		return failure("unavailable", true)
	}
	var payload struct {
		Meta struct {
			Code  int `json:"code"`
			Error *struct {
				Type string `json:"type"`
			} `json:"error"`
		} `json:"meta"`
		Result *struct {
			Items []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Type    string `json:"type"`
				Address string `json:"address_name"`
				Point   *struct {
					Latitude  *float64 `json:"lat"`
					Longitude *float64 `json:"lon"`
				} `json:"point"`
				Rubrics []struct {
					ID string `json:"id"`
				} `json:"rubrics"`
			} `json:"items"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return failure("unavailable", true)
	}
	switch payload.Meta.Code {
	case 401, 403:
		return failure("access_denied", false)
	case 429:
		return failure("rate_limited", true)
	case 404:
		if payload.Meta.Error != nil && payload.Meta.Error.Type == "itemNotFound" && payload.Result == nil {
			return failure("not_found", false)
		}
	}
	if response.StatusCode != http.StatusOK || payload.Meta.Code != 200 || payload.Meta.Error != nil || payload.Result == nil || len(payload.Result.Items) != 1 {
		return failure("unavailable", true)
	}
	item := payload.Result.Items[0]
	food := false
	for _, rubric := range item.Rubrics {
		if rubric.ID == "161" { // 2GIS cafe rubric, verified against the byid result.
			food = true
		}
	}
	if item.ID != externalID || item.Type != "branch" || !validText(item.Name, 500, false) || !validText(item.Address, 1000, true) || item.Point == nil || item.Point.Latitude == nil || item.Point.Longitude == nil {
		return failure("unavailable", true)
	}
	if !food {
		return failure("not_food", false)
	}
	position := d.Coordinate{Latitude: *item.Point.Latitude, Longitude: *item.Point.Longitude}
	if position.Validate() != nil {
		return failure("unavailable", true)
	}
	return Organization{Provider: "2gis", ExternalID: item.ID, Title: strings.TrimSpace(item.Name), Address: strings.TrimSpace(item.Address),
		Position: position, ObservedAt: time.Now().UTC(), PriceStatus: "unknown", AvailabilityStatus: "unknown", HoursStatus: "unknown"}, nil
}

func validText(value string, limit int, empty bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || !empty && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
