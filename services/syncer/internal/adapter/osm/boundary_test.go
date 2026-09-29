package osm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

const permRelation = `{"osm3s":{"timestamp_osm_base":"2026-09-29T12:58:50Z"},"elements":[{"type":"relation",
"id":1084793,"members":[
{"type":"node","ref":1,"role":"admin_centre","lat":58.01,"lon":56.25},
{"type":"way","ref":2,"role":"outer","geometry":[{"lat":57.9,"lon":56.0},{"lat":57.9,"lon":56.5},{"lat":58.1,"lon":56.5}]},
{"type":"way","ref":3,"role":"outer","geometry":[{"lat":58.1,"lon":56.5},{"lat":58.1,"lon":56.0},{"lat":57.9,"lon":56.0}]},
{"type":"relation","ref":4,"role":"subarea"}]}]}`

// overpass answers each request with the next reply; the last reply repeats.
func overpass(t *testing.T, replies ...func(w http.ResponseWriter)) (*Adapter, *int, *string) {
	t.Helper()
	requests, query := 0, ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.FormValue("data")
		replies[min(requests, len(replies)-1)](w)
		requests++
	}))
	t.Cleanup(server.Close)
	a := newAdapter(server.URL)
	a.retryDelays = []time.Duration{0, 0}
	return a, &requests, &query
}

func reply(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck // test server
	}
}

func TestBoundaryReadsTheRelationLines(t *testing.T) {
	a, _, query := overpass(t, reply(http.StatusOK, permRelation))
	b, err := a.Boundary(context.Background(), domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	want := CityBoundary{RelationID: 1084793, OSMBase: "2026-09-29T12:58:50Z", Lines: [][][2]float64{
		{{56.0, 57.9}, {56.5, 57.9}, {56.5, 58.1}},
		{{56.5, 58.1}, {56.0, 58.1}, {56.0, 57.9}},
	}}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("boundary %+v", b)
	}
	if !strings.Contains(*query, `rel["name"="Пермский городской округ"]["boundary"="administrative"];out geom;`) ||
		strings.Contains(*query, "meta") {
		t.Fatalf("query %s", *query)
	}
}

func TestBoundaryRetriesABusyServer(t *testing.T) {
	a, requests, _ := overpass(t,
		reply(http.StatusGatewayTimeout, "timeout"),
		reply(http.StatusOK, "<html>The server is probably too busy to handle your request.</html>"),
		reply(http.StatusOK, permRelation))
	if _, err := a.Boundary(context.Background(), domain.Perm); err != nil || *requests != 3 {
		t.Fatalf("requests %d, err %v", *requests, err)
	}
}

func TestBoundaryGivesUpAfterThreeAttempts(t *testing.T) {
	a, requests, _ := overpass(t, reply(http.StatusGatewayTimeout, "timeout"))
	_, err := a.Boundary(context.Background(), domain.Perm)
	var fetchErr *ingest.FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != "http_status_504" || *requests != 3 {
		t.Fatalf("requests %d, err %v", *requests, err)
	}
}

func TestBoundaryDoesNotRetryAnswersThatWillNotChange(t *testing.T) {
	cases := map[string]string{
		"http_status_400":        "",
		"boundary_relations":     `{"elements":[]}`,
		"boundary_without_lines": `{"elements":[{"type":"relation","id":1,"members":[{"type":"node","ref":1}]}]}`,
	}
	for code, body := range cases {
		status := http.StatusOK
		if body == "" {
			status = http.StatusBadRequest
		}
		a, requests, _ := overpass(t, reply(status, body))
		_, err := a.Boundary(context.Background(), domain.Moscow)
		var fetchErr *ingest.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != code || *requests != 1 {
			t.Errorf("%s: requests %d, err %v", code, *requests, err)
		}
	}
}
