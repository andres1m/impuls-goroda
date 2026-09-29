package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lunchprovider"
	"github.com/labstack/echo/v5"
)

type LunchSearchRuntime interface {
	Authenticator
	OwnerChecker
}

type LunchSearchRouter struct {
	key     string
	runtime LunchSearchRuntime
	client  *http.Client
	slots   chan struct{}
	details *lunchprovider.Client
}

func NewLunchSearchRouter(key string, runtime LunchSearchRuntime) *LunchSearchRouter {
	details, _ := lunchprovider.NewClient(key)
	return &LunchSearchRouter{key: strings.TrimSpace(key), runtime: runtime, slots: make(chan struct{}, 4), details: details,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (r *LunchSearchRouter) Routes() []router.Route {
	return []router.Route{router.NewRoute(http.MethodPost, "/routes/:route_id/lunch/search", func() echo.HandlerFunc { return r.search },
		shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodGet, "/routes/:route_id/lunch/organizations/:external_id", func() echo.HandlerFunc { return r.organization },
			shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime))}
}

type lunchPosition struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

type lunchCandidate struct {
	Provider           string        `json:"provider"`
	ExternalID         string        `json:"external_id"`
	Title              string        `json:"title"`
	Address            string        `json:"address,omitempty"`
	Position           lunchPosition `json:"position"`
	DistanceMeters     int           `json:"distance_meters"`
	ObservedAt         time.Time     `json:"observed_at"`
	PriceStatus        string        `json:"price_status"`
	AvailabilityStatus string        `json:"availability_status"`
	HoursStatus        string        `json:"hours_status"`
}

type lunchProviderResult struct {
	Meta struct {
		Code  int `json:"code"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	} `json:"meta"`
	Result *struct {
		Items []struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Type    string   `json:"type"`
			Address string   `json:"address_name"`
			Lat     *float64 `json:"lat"`
			Lon     *float64 `json:"lon"`
			Point   *struct {
				Lon *float64 `json:"lon"`
				Lat *float64 `json:"lat"`
			} `json:"point"`
		} `json:"items"`
	} `json:"result"`
}

func (r *LunchSearchRouter) search(c *echo.Context) error {
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
	var input struct {
		Position *struct {
			Longitude *float64 `json:"longitude"`
			Latitude  *float64 `json:"latitude"`
		} `json:"position"`
		Radius int `json:"radius_meters"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return malformedCommandHeader()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || input.Position == nil || input.Position.Longitude == nil || input.Position.Latitude == nil {
		return malformedCommandHeader()
	}
	point := lunchPosition{Longitude: *input.Position.Longitude, Latitude: *input.Position.Latitude}
	if !validCoordinate([]float64{point.Latitude, point.Longitude}) || !validLunchRadius(input.Radius) {
		return malformedCommandHeader()
	}
	if r.key == "" {
		return lunchSearchError("LUNCH_SEARCH_NOT_CONFIGURED", false)
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return lunchSearchError("LUNCH_SEARCH_BUSY", true)
	}
	candidates, observed, err := r.nearby(c, point, input.Radius)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, struct {
		RequestID  string           `json:"request_id"`
		SearchedAt time.Time        `json:"searched_at"`
		Radius     int              `json:"radius_meters"`
		Candidates []lunchCandidate `json:"candidates"`
	}{RequestID(c), observed, input.Radius, candidates})
}

func (r *LunchSearchRouter) nearby(c *echo.Context, point lunchPosition, radius int) ([]lunchCandidate, time.Time, error) {
	items, observed, err := r.nearbyFrom(c, point, radius, true)
	if err == nil && len(items) > 0 {
		return items, observed, nil
	}
	return r.nearbyFrom(c, point, radius, false)
}

func (r *LunchSearchRouter) nearbyFrom(c *echo.Context, point lunchPosition, radius int, markers bool) ([]lunchCandidate, time.Time, error) {
	status, providerCode := 0, 0
	failed := func() ([]lunchCandidate, time.Time, error) {
		err := lunchSearchError("LUNCH_SEARCH_UNAVAILABLE", true)
		err.Cause = fmt.Errorf("2GIS lunch response: HTTP %d, provider code %d", status, providerCode)
		return nil, time.Time{}, err
	}
	position := strconv.FormatFloat(point.Longitude, 'f', -1, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', -1, 64)
	endpoint := url.URL{Scheme: "https", Host: "catalog.api.2gis.com", Path: "/3.0/items"}
	query := url.Values{"key": {r.key}, "q": {"кафе"}, "type": {"branch"}, "point": {position}, "location": {position},
		"radius": {strconv.Itoa(radius)}, "sort": {"distance"}, "page_size": {"20"}, "fields": {"items.point"}, "locale": {"ru_RU"}}
	if markers {
		endpoint.Path = "/3.0/markers"
		query.Del("page_size")
		query.Set("limit", "20")
		query.Set("fields", "items.name")
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(c.Request().Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return failed()
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return failed()
	}
	defer response.Body.Close()
	status = response.StatusCode
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, time.Time{}, lunchSearchError("LUNCH_SEARCH_ACCESS_DENIED", false)
	}
	if response.StatusCode == 429 {
		return nil, time.Time{}, lunchSearchError("LUNCH_SEARCH_RATE_LIMITED", true)
	}
	const limit = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) > limit {
		return failed()
	}
	var payload lunchProviderResult
	if err := json.Unmarshal(body, &payload); err != nil {
		return failed()
	}
	providerCode = payload.Meta.Code
	if payload.Meta.Code == 404 && payload.Meta.Error != nil && payload.Meta.Error.Type == "itemNotFound" && payload.Result == nil {
		return []lunchCandidate{}, time.Now().UTC(), nil
	}
	if payload.Meta.Code == 401 || payload.Meta.Code == 403 {
		return nil, time.Time{}, lunchSearchError("LUNCH_SEARCH_ACCESS_DENIED", false)
	}
	if payload.Meta.Code == 429 {
		return nil, time.Time{}, lunchSearchError("LUNCH_SEARCH_RATE_LIMITED", true)
	}
	if response.StatusCode != http.StatusOK || payload.Meta.Code != 200 || payload.Meta.Error != nil || payload.Result == nil || payload.Result.Items == nil || len(payload.Result.Items) > 2000 {
		return failed()
	}
	observed := time.Now().UTC()
	items := make([]lunchCandidate, 0, len(payload.Result.Items))
	seen := make(map[string]bool)
	usable := 0
	for _, item := range payload.Result.Items {
		id, title := strings.TrimSpace(item.ID), strings.TrimSpace(item.Name)
		lat, lon := item.Lat, item.Lon
		if item.Point != nil {
			lat, lon = item.Point.Lat, item.Point.Lon
		}
		if (item.Type != "branch" && !(markers && item.Type == "")) || id == "" || utf8.RuneCountInString(id) > 128 || title == "" || utf8.RuneCountInString(title) > 500 ||
			utf8.RuneCountInString(item.Address) > 1000 || lat == nil || lon == nil {
			continue
		}
		target := lunchPosition{Longitude: *lon, Latitude: *lat}
		if !validCoordinate([]float64{target.Latitude, target.Longitude}) {
			continue
		}
		usable++
		distance := lunchDistance(point, target)
		if distance > float64(radius) || seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, lunchCandidate{Provider: "2gis", ExternalID: id, Title: title, Address: item.Address, Position: target,
			DistanceMeters: int(math.Round(distance)), ObservedAt: observed, PriceStatus: "unknown", AvailabilityStatus: "unknown", HoursStatus: "unknown"})
	}
	if len(payload.Result.Items) > 0 && usable == 0 {
		return failed()
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DistanceMeters == items[j].DistanceMeters {
			return items[i].ExternalID < items[j].ExternalID
		}
		return items[i].DistanceMeters < items[j].DistanceMeters
	})
	return items[:min(len(items), 20)], observed, nil
}

func validLunchRadius(radius int) bool {
	return radius == 300 || radius == 500 || radius == 800 || radius == 1000
}

func lunchDistance(a, b lunchPosition) float64 {
	const radians = math.Pi / 180
	lat := (b.Latitude - a.Latitude) * radians
	lon := (b.Longitude - a.Longitude) * radians
	h := math.Pow(math.Sin(lat/2), 2) + math.Cos(a.Latitude*radians)*math.Cos(b.Latitude*radians)*math.Pow(math.Sin(lon/2), 2)
	return 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, math.Max(0, h))))
}

func lunchSearchError(code string, retryable bool) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: code, Message: "Nearby lunch search is unavailable", Retryable: retryable}
}
