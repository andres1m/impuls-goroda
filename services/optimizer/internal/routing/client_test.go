package routing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	a = domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	b = domain.Coordinate{Longitude: 56.26, Latitude: 58.02}
)

func osrm(t *testing.T, status int, body string, seen *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.URL.String()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

const okTable = `{"code":"Ok","data_version":"20260926T202251Z-13fd215c",
	"durations":[[0,120.5],[null,0]],
	"distances":[[0,800],[null,0]],
	"sources":[{"distance":4.2},{"distance":620}]}`

func TestTable(t *testing.T) {
	var seen string
	server := osrm(t, http.StatusOK, okTable, &seen)
	table, err := NewClient(server.Client()).Table(context.Background(), server.URL, "foot", []domain.Coordinate{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "/table/v1/foot/56.229400,58.010500;56.260000,58.020000?annotations=duration,distance&generate_hints=false" {
		t.Fatalf("request %s", seen)
	}
	if table.Version != "20260926T202251Z-13fd215c" || len(table.SnapMeters) != 2 || table.SnapMeters[1] != 620 {
		t.Fatalf("table = %+v", table)
	}
	if seconds, meters, ok := table.Pair(0, 1); !ok || seconds != 120.5 || meters != 800 {
		t.Fatalf("pair 0→1 = %v %v %v", seconds, meters, ok)
	}
	if _, _, ok := table.Pair(1, 0); ok {
		t.Fatal("unreachable pair reported reachable")
	}
}

func TestTableRejectsBadResponses(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"router error": {
			http.StatusBadRequest,
			`{"code":"InvalidQuery","message":"Query string malformed"}`,
			"InvalidQuery",
		},
		"proxy error": {http.StatusServiceUnavailable, `{}`, "503"},
		"not ok":      {http.StatusOK, `{"code":"NoTable","message":"none"}`, "NoTable"},
		"no data version": {
			http.StatusOK,
			strings.Replace(okTable, `"data_version":"20260926T202251Z-13fd215c",`, "", 1),
			"data version",
		},
		"wrong size": {http.StatusOK, strings.Replace(okTable, `[[0,120.5],[null,0]]`, `[[0,120.5]]`, 1), "size"},
		"no sources": {
			http.StatusOK,
			strings.Replace(okTable, `"sources":[{"distance":4.2},{"distance":620}]`, `"sources":[]`, 1),
			"size",
		},
		"not json": {http.StatusOK, `<html>`, "decode"},
	}
	for name, tc := range cases {
		server := osrm(t, tc.status, tc.body, nil)
		_, err := NewClient(server.Client()).Table(context.Background(), server.URL, "foot", []domain.Coordinate{a, b})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTableRequiresPoints(t *testing.T) {
	if _, err := NewClient(http.DefaultClient).Table(context.Background(), "http://unused", "foot", nil); err == nil {
		t.Fatal("empty table accepted")
	}
}

func TestTableHonoursDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewClient(server.Client()).Table(ctx, server.URL, "foot", []domain.Coordinate{a, b}); err == nil {
		t.Fatal("slow router did not fail the request")
	}
}
