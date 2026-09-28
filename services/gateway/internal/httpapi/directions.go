package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/labstack/echo/v5"
)

const maxDirectionsBodyBytes = 2048
const maxDirectionsResponseBytes = 4 << 20

type DirectionsRouter struct {
	key     string
	client  *http.Client
	limiter AnonymousLimiter
}

func NewDirectionsRouter(key string, limiter AnonymousLimiter) *DirectionsRouter {
	return &DirectionsRouter{key: key, client: &http.Client{Timeout: 8 * time.Second}, limiter: limiter}
}

func (r *DirectionsRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/prototype/directions", r.directions, AnonymousRateLimit(r.limiter)),
	}
}

type directionsRequest struct {
	Points [][]float64 `json:"points"`
	Mode   string      `json:"mode"`
}

type directionsResponse struct {
	Segments [][][]float64 `json:"segments"`
}

type twoGisPoint struct {
	Type string  `json:"type,omitempty"`
	Lon  float64 `json:"lon"`
	Lat  float64 `json:"lat"`
}

type twoGisRouteResponse struct {
	Status string `json:"status"`
	Result []struct {
		BeginPedestrianPath struct {
			Geometry struct {
				Selection string `json:"selection"`
			} `json:"geometry"`
		} `json:"begin_pedestrian_path"`
		EndPedestrianPath struct {
			Geometry struct {
				Selection string `json:"selection"`
			} `json:"geometry"`
		} `json:"end_pedestrian_path"`
		Maneuvers []struct {
			OutcomingPath struct {
				Geometry []struct {
					Selection string `json:"selection"`
				} `json:"geometry"`
			} `json:"outcoming_path"`
		} `json:"maneuvers"`
	} `json:"result"`
}

type twoGisTransitRoute struct {
	Movements []struct {
		Type         string `json:"type"`
		Alternatives []struct {
			Geometry []struct {
				Selection string `json:"selection"`
			} `json:"geometry"`
		} `json:"alternatives"`
	} `json:"movements"`
}

func (r *DirectionsRouter) directions() echo.HandlerFunc {
	return func(c *echo.Context) error {
		if r.key == "" {
			return directionsUnavailable()
		}
		var input directionsRequest
		decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, maxDirectionsBodyBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return directionsMalformed()
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || !validDirections(input) {
			return directionsMalformed()
		}

		segments := make([][][]float64, 0)
		groupSize := len(input.Points)
		if input.Mode == "walking" {
			groupSize = 5
		}
		for start := 0; start < len(input.Points)-1; start += groupSize - 1 {
			end := start + groupSize
			if end > len(input.Points) {
				end = len(input.Points)
			}
			part, err := r.routing(c, input.Points[start:end], input.Mode)
			if err != nil {
				return err
			}
			segments = append(segments, part...)
		}
		if len(segments) == 0 {
			return directionsUnavailable()
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, directionsResponse{Segments: segments})
	}
}

func (r *DirectionsRouter) routing(c *echo.Context, points [][]float64, mode string) ([][][]float64, error) {
	path := "/routing/7.0.0/global"
	var body any
	if mode == "transit" {
		path = "/public_transport/2.0"
		payload := map[string]any{
			"source":    map[string]any{"point": twoGisPoint{Lon: points[0][1], Lat: points[0][0]}},
			"target":    map[string]any{"point": twoGisPoint{Lon: points[len(points)-1][1], Lat: points[len(points)-1][0]}},
			"transport": []string{"pedestrian", "bus", "tram", "trolleybus", "shuttle_bus", "metro", "light_metro", "suburban_train", "aeroexpress", "monorail", "funicular_railway", "river_transport", "cable_car", "light_rail", "premetro", "mcc", "mcd"},
			"locale":    "ru", "max_result_count": 1,
		}
		if len(points) > 2 {
			intermediate := make([]map[string]any, 0, len(points)-2)
			for _, point := range points[1 : len(points)-1] {
				intermediate = append(intermediate, map[string]any{"point": twoGisPoint{Lon: point[1], Lat: point[0]}})
			}
			payload["intermediate_points"] = intermediate
		}
		body = payload
	} else {
		routePoints := make([]twoGisPoint, len(points))
		for i, point := range points {
			kind := "pref"
			if i == 0 || i == len(points)-1 {
				kind = "stop"
				if mode == "walking" {
					kind = "walking"
				}
			}
			routePoints[i] = twoGisPoint{Type: kind, Lon: point[1], Lat: point[0]}
		}
		transport := "driving"
		if mode == "walking" {
			transport = "walking"
		}
		body = map[string]any{"points": routePoints, "transport": transport, "output": "detailed", "locale": "ru"}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, directionsUnavailable()
	}
	endpoint := url.URL{Scheme: "https", Host: "routing.api.2gis.com", Path: path}
	query := endpoint.Query()
	query.Set("key", r.key)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(c.Request().Context(), http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return nil, directionsUnavailable()
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, directionsUnavailable()
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "DIRECTIONS_ACCESS_DENIED", Message: "Directions provider rejected the request"}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "DIRECTIONS_RATE_LIMITED", Message: "Directions provider rate limit reached", Retryable: true}
	}
	if response.StatusCode == http.StatusUnprocessableEntity || response.StatusCode == http.StatusBadRequest {
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "DIRECTIONS_REJECTED_REQUEST", Message: "Directions provider rejected route points"}
	}
	if response.StatusCode != http.StatusOK {
		return nil, directionsUnavailable()
	}
	limited := io.LimitReader(response.Body, maxDirectionsResponseBytes)
	if mode == "transit" {
		var routes []twoGisTransitRoute
		if err := json.NewDecoder(limited).Decode(&routes); err != nil || len(routes) == 0 {
			return nil, directionsUnavailable()
		}
		segments := make([][][]float64, 0)
		for index, movement := range routes[0].Movements {
			if len(movement.Alternatives) == 0 {
				if movement.Type == "passage" || index == len(routes[0].Movements)-1 {
					continue
				}
				return nil, directionsUnavailable()
			}
			if len(movement.Alternatives[0].Geometry) == 0 {
				return nil, directionsUnavailable()
			}
			for _, geometry := range movement.Alternatives[0].Geometry {
				segment, err := parseLineString(geometry.Selection)
				if err != nil {
					return nil, directionsUnavailable()
				}
				segments = append(segments, segment)
			}
		}
		if len(segments) == 0 {
			return nil, directionsUnavailable()
		}
		return segments, nil
	}
	var routes twoGisRouteResponse
	if err := json.NewDecoder(limited).Decode(&routes); err != nil {
		return nil, directionsUnavailable()
	}
	if routes.Status == "ROUTE_NOT_FOUND" || routes.Status == "ROUTE_DOES_NOT_EXISTS" || routes.Status == "ATTRACT_FAIL" {
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "DIRECTIONS_NO_ROUTE", Message: "Directions provider found no route"}
	}
	if routes.Status != "OK" || len(routes.Result) == 0 {
		return nil, directionsUnavailable()
	}
	chosen := routes.Result[0]
	selections := make([]string, 0)
	if chosen.BeginPedestrianPath.Geometry.Selection != "" {
		selections = append(selections, chosen.BeginPedestrianPath.Geometry.Selection)
	}
	for _, maneuver := range chosen.Maneuvers {
		for _, geometry := range maneuver.OutcomingPath.Geometry {
			selections = append(selections, geometry.Selection)
		}
	}
	if chosen.EndPedestrianPath.Geometry.Selection != "" {
		selections = append(selections, chosen.EndPedestrianPath.Geometry.Selection)
	}
	if len(selections) == 0 {
		return nil, directionsUnavailable()
	}
	segments := make([][][]float64, 0, len(selections))
	for _, selection := range selections {
		segment, err := parseLineString(selection)
		if err != nil {
			return nil, directionsUnavailable()
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func parseLineString(value string) ([][]float64, error) {
	if !strings.HasPrefix(value, "LINESTRING(") || !strings.HasSuffix(value, ")") {
		return nil, errors.New("invalid geometry")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "LINESTRING("), ")"), ",")
	if len(parts) < 2 {
		return nil, errors.New("short geometry")
	}
	points := make([][]float64, 0, len(parts))
	for _, part := range parts {
		coordinates := strings.Fields(part)
		if len(coordinates) != 2 && len(coordinates) != 3 {
			return nil, errors.New("invalid point")
		}
		lon, err := strconv.ParseFloat(coordinates[0], 64)
		if err != nil {
			return nil, err
		}
		lat, err := strconv.ParseFloat(coordinates[1], 64)
		if err != nil || !validCoordinate([]float64{lat, lon}) {
			return nil, errors.New("invalid point")
		}
		if len(coordinates) == 3 {
			altitude, err := strconv.ParseFloat(coordinates[2], 64)
			if err != nil || math.IsNaN(altitude) || math.IsInf(altitude, 0) {
				return nil, errors.New("invalid altitude")
			}
		}
		points = append(points, []float64{lat, lon})
	}
	return points, nil
}

func validDirections(input directionsRequest) bool {
	if len(input.Points) < 2 || len(input.Points) > 10 {
		return false
	}
	switch input.Mode {
	case "walking", "transit", "driving":
	default:
		return false
	}
	for _, point := range input.Points {
		if !validCoordinate(point) {
			return false
		}
	}
	return true
}

func validCoordinate(point []float64) bool {
	return len(point) == 2 && !math.IsNaN(point[0]) && !math.IsNaN(point[1]) &&
		!math.IsInf(point[0], 0) && !math.IsInf(point[1], 0) &&
		point[0] >= -90 && point[0] <= 90 && point[1] >= -180 && point[1] <= 180
}

func directionsMalformed() error {
	return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
}

func directionsUnavailable() error {
	return &Error{Status: http.StatusServiceUnavailable, Code: "DIRECTIONS_UNAVAILABLE", Message: "Directions are temporarily unavailable", Retryable: true}
}
