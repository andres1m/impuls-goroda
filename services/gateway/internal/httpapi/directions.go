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

const (
	maxDirectionsBodyBytes     = 2048
	maxDirectionsResponseBytes = 4 << 20
	directionsTimeout          = 8 * time.Second
	walkingGroupSize           = 5
	minRoutePoints             = 2
	maxRoutePoints             = 10
	pointCoords2D              = 2
	pointCoords3D              = 3
	modeWalking                = "walking"
	modeTransit                = "transit"
	modeDriving                = "driving"
	pointKey                   = "point"
)

type DirectionsRouter struct {
	key     string
	client  *http.Client
	limiter AnonymousLimiter
}

func NewDirectionsRouter(key string, limiter AnonymousLimiter) *DirectionsRouter {
	return &DirectionsRouter{key: key, client: &http.Client{Timeout: directionsTimeout}, limiter: limiter}
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
		input, err := decodeDirectionsRequest(c)
		if err != nil {
			return err
		}

		segments := make([][][]float64, 0)
		groupSize := len(input.Points)
		if input.Mode == modeWalking {
			groupSize = walkingGroupSize
		}
		for start := 0; start < len(input.Points)-1; start += groupSize - 1 {
			end := min(start+groupSize, len(input.Points))
			part, routeErr := r.routing(c, input.Points[start:end], input.Mode)
			if routeErr != nil {
				return routeErr
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

func decodeDirectionsRequest(c *echo.Context) (directionsRequest, error) {
	var input directionsRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, maxDirectionsBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return directionsRequest{}, directionsMalformed()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || !validDirections(input) {
		return directionsRequest{}, directionsMalformed()
	}
	return input, nil
}

func (r *DirectionsRouter) routing(c *echo.Context, points [][]float64, mode string) ([][][]float64, error) {
	path, body := buildRoutingPayload(points, mode)
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, directionsUnavailable()
	}
	endpoint := url.URL{Scheme: "https", Host: "routing.api.2gis.com", Path: path}
	query := endpoint.Query()
	query.Set("key", r.key)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(
		c.Request().Context(),
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(encoded),
	)
	if err != nil {
		return nil, directionsUnavailable()
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, directionsUnavailable()
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = directionsUnavailable()
		}
	}()
	if statusErr := checkRoutingStatus(response.StatusCode); statusErr != nil {
		return nil, statusErr
	}
	limited := io.LimitReader(response.Body, maxDirectionsResponseBytes)
	if mode == modeTransit {
		return decodeTransitSegments(limited)
	}
	return decodeStandardSegments(limited)
}

func buildRoutingPayload(points [][]float64, mode string) (endpoint string, payload any) {
	if mode == modeTransit {
		return "/public_transport/2.0", buildTransitPayload(points)
	}
	return "/routing/7.0.0/global", buildStandardPayload(points, mode)
}

func buildTransitPayload(points [][]float64) map[string]any {
	payload := map[string]any{
		"source": map[string]any{pointKey: twoGisPoint{Lon: points[0][1], Lat: points[0][0]}},
		"target": map[string]any{
			pointKey: twoGisPoint{Lon: points[len(points)-1][1], Lat: points[len(points)-1][0]},
		},
		"transport": []string{
			"pedestrian",
			"bus",
			"tram",
			"trolleybus",
			"shuttle_bus",
			"metro",
			"light_metro",
			"suburban_train",
			"aeroexpress",
			"monorail",
			"funicular_railway",
			"river_transport",
			"cable_car",
			"light_rail",
			"premetro",
			"mcc",
			"mcd",
		},
		"locale":           "ru",
		"max_result_count": 1,
	}
	if len(points) > minRoutePoints {
		intermediate := make([]map[string]any, 0, len(points)-minRoutePoints)
		for _, point := range points[1 : len(points)-1] {
			intermediate = append(intermediate, map[string]any{pointKey: twoGisPoint{Lon: point[1], Lat: point[0]}})
		}
		payload["intermediate_points"] = intermediate
	}
	return payload
}

func buildStandardPayload(points [][]float64, mode string) map[string]any {
	routePoints := make([]twoGisPoint, len(points))
	for i, point := range points {
		kind := "pref"
		if i == 0 || i == len(points)-1 {
			kind = "stop"
			if mode == modeWalking {
				kind = modeWalking
			}
		}
		routePoints[i] = twoGisPoint{Type: kind, Lon: point[1], Lat: point[0]}
	}
	transport := modeDriving
	if mode == modeWalking {
		transport = modeWalking
	}
	return map[string]any{"points": routePoints, "transport": transport, "output": "detailed", "locale": "ru"}
}

func checkRoutingStatus(statusCode int) error {
	switch statusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "DIRECTIONS_ACCESS_DENIED",
			Message: "Directions provider rejected the request",
		}
	case http.StatusTooManyRequests:
		return &Error{
			Status:    http.StatusServiceUnavailable,
			Code:      "DIRECTIONS_RATE_LIMITED",
			Message:   "Directions provider rate limit reached",
			Retryable: true,
		}
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "DIRECTIONS_REJECTED_REQUEST",
			Message: "Directions provider rejected route points",
		}
	default:
		return directionsUnavailable()
	}
}

func decodeTransitSegments(limited io.Reader) ([][][]float64, error) {
	var routes []twoGisTransitRoute
	if err := json.NewDecoder(limited).Decode(&routes); err != nil || len(routes) == 0 {
		return nil, directionsUnavailable()
	}
	segments := make([][][]float64, 0)
	movements := routes[0].Movements
	for index, movement := range movements {
		if len(movement.Alternatives) == 0 {
			if movement.Type == "passage" || index == len(movements)-1 {
				continue
			}
			return nil, directionsUnavailable()
		}
		part, err := extractGeometrySegments(movement.Alternatives[0].Geometry)
		if err != nil {
			return nil, err
		}
		segments = append(segments, part...)
	}
	if len(segments) == 0 {
		return nil, directionsUnavailable()
	}
	return segments, nil
}

func extractGeometrySegments(geometries []struct {
	Selection string `json:"selection"`
}) ([][][]float64, error) {
	if len(geometries) == 0 {
		return nil, directionsUnavailable()
	}
	segments := make([][][]float64, 0, len(geometries))
	for _, geometry := range geometries {
		segment, err := parseLineString(geometry.Selection)
		if err != nil {
			return nil, directionsUnavailable()
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func decodeStandardSegments(limited io.Reader) ([][][]float64, error) {
	var routes twoGisRouteResponse
	if err := json.NewDecoder(limited).Decode(&routes); err != nil {
		return nil, directionsUnavailable()
	}
	if routes.Status == "ROUTE_NOT_FOUND" || routes.Status == "ROUTE_DOES_NOT_EXISTS" ||
		routes.Status == "ATTRACT_FAIL" {
		return nil, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "DIRECTIONS_NO_ROUTE",
			Message: "Directions provider found no route",
		}
	}
	if routes.Status != "OK" || len(routes.Result) == 0 {
		return nil, directionsUnavailable()
	}
	selections := collectRouteSelections(&routes)
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

func collectRouteSelections(routes *twoGisRouteResponse) []string {
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
	return selections
}

func parseLineString(value string) ([][]float64, error) {
	if !strings.HasPrefix(value, "LINESTRING(") || !strings.HasSuffix(value, ")") {
		return nil, errors.New("invalid geometry")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "LINESTRING("), ")"), ",")
	if len(parts) < minRoutePoints {
		return nil, errors.New("short geometry")
	}
	points := make([][]float64, 0, len(parts))
	for _, part := range parts {
		pt, err := parseCoordinatePoint(part)
		if err != nil {
			return nil, err
		}
		points = append(points, pt)
	}
	return points, nil
}

func parseCoordinatePoint(part string) ([]float64, error) {
	coordinates := strings.Fields(part)
	if len(coordinates) != pointCoords2D && len(coordinates) != pointCoords3D {
		return nil, errors.New("invalid point")
	}
	lon, err := strconv.ParseFloat(coordinates[0], 64)
	if err != nil {
		return nil, errors.New("invalid point")
	}
	lat, err := strconv.ParseFloat(coordinates[1], 64)
	if err != nil || !validCoordinate([]float64{lat, lon}) {
		return nil, errors.New("invalid point")
	}
	if len(coordinates) == pointCoords3D {
		altitude, altErr := strconv.ParseFloat(coordinates[2], 64)
		if altErr != nil || math.IsNaN(altitude) || math.IsInf(altitude, 0) {
			return nil, errors.New("invalid altitude")
		}
	}
	return []float64{lat, lon}, nil
}

func validDirections(input directionsRequest) bool {
	if len(input.Points) < minRoutePoints || len(input.Points) > maxRoutePoints {
		return false
	}
	switch input.Mode {
	case modeWalking, modeTransit, modeDriving:
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
	return len(point) == pointCoords2D && !math.IsNaN(point[0]) && !math.IsNaN(point[1]) &&
		!math.IsInf(point[0], 0) && !math.IsInf(point[1], 0) &&
		point[0] >= -90 && point[0] <= 90 && point[1] >= -180 && point[1] <= 180
}

func directionsMalformed() error {
	return &Error{Status: http.StatusBadRequest, Code: codeMalformedRequest, Message: msgMalformedRequest}
}

func directionsUnavailable() error {
	return &Error{
		Status:    http.StatusServiceUnavailable,
		Code:      "DIRECTIONS_UNAVAILABLE",
		Message:   "Directions are temporarily unavailable",
		Retryable: true,
	}
}
