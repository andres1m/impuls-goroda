package osm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

var fixedNow = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

const museumNode = `{"type":"node","id":702713073,"lat":58.0043029,"lon":56.2597168,"tags":{"name":"Музей","tourism":"museum"}}`

const museumWay = `{"type":"way","id":25995453,"center":{"lat":58.0411115,"lon":56.3201269},"tags":{"name":"Музей-Диорама","opening_hours":"We-Su 10:00-18:00","tourism":"museum"}}`

func newAdapter(url string) *Adapter {
	return New(url, &http.Client{Timeout: 5 * time.Second}, func() time.Time { return fixedNow })
}

func TestFetchParsesElements(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != interpreterPath || r.Header.Get("User-Agent") != ingest.UserAgent {
			http.Error(w, "unexpected request", http.StatusNotAcceptable)
			return
		}
		gotQuery = r.FormValue("data")
		w.Write([]byte(`{"version":0.6,"osm3s":{"timestamp_osm_base":"2026-09-26T22:12:34Z"},"elements":[` +
			museumNode + `,` + museumWay + `,{"type":"node","tags":{}}]}`))
	}))
	defer server.Close()

	batch, err := newAdapter(server.URL).Fetch(context.Background(), domain.Perm, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`area["name"="Пермский городской округ"]["boundary"="administrative"]`, `out tags center;`, `"tourism"~`, `"amenity"~`, `"leisure"~`} {
		if !strings.Contains(gotQuery, part) {
			t.Fatalf("query lacks %s: %s", part, gotQuery)
		}
	}
	if strings.Contains(gotQuery, "meta") {
		t.Fatal("query must not request contributor metadata")
	}
	if len(batch.Records) != 2 || batch.Skipped != 1 {
		t.Fatalf("%d records, %d skipped", len(batch.Records), batch.Skipped)
	}
	node, way := batch.Records[0], batch.Records[1]
	if node.ExternalID != "node/702713073" || node.SourceURL != "https://www.openstreetmap.org/node/702713073" || string(node.Payload) != museumNode {
		t.Fatalf("node = %+v", node)
	}
	if way.ExternalID != "way/25995453" || way.SourceURL != "https://www.openstreetmap.org/way/25995453" || way.SourceUpdatedAt != nil {
		t.Fatalf("way = %+v", way)
	}
	var cursor map[string]string
	if err := json.Unmarshal(batch.Cursor, &cursor); err != nil ||
		cursor["snapshot_at"] != "2026-09-27T10:00:00Z" || cursor["osm_base"] != "2026-09-26T22:12:34Z" {
		t.Fatalf("cursor = %s", batch.Cursor)
	}
}

func TestMoscowQueryUsesRegionArea(t *testing.T) {
	if q := query(domain.Moscow); !strings.Contains(q, `area["name"="Москва"]["admin_level"="4"]`) {
		t.Fatalf("query = %s", q)
	}
}

func TestFetchErrorCodes(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http_status_406": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotAcceptable) },
		"decode": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<?xml version="1.0"?><html><body>The server is probably too busy</body></html>`))
		},
		"overpass_remark": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"osm3s":{},"elements":[],"remark":"runtime error: Query timed out"}`))
		},
	}
	for code, handler := range cases {
		server := httptest.NewServer(handler)
		_, err := newAdapter(server.URL).Fetch(context.Background(), domain.Perm, nil)
		server.Close()
		var fetchErr *ingest.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != code {
			t.Fatalf("want %s, got %v", code, err)
		}
	}
}

func TestSourceIsLive(t *testing.T) {
	s := newAdapter("http://unused").Source()
	if s.Key != domain.OSM || s.DataMode != domain.Live || s.AccessMode != domain.AccessAPI {
		t.Fatalf("source = %+v", s)
	}
}
