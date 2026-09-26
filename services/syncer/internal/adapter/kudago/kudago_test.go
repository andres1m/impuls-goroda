package kudago

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

var fixedNow = time.Unix(1790460000, 0).UTC()

const firstEvent = `{"id":226287,"title":"концерт «„Детский альбом“ Чайковского»","price":"от 400 до 700 рублей","is_free":false,"site_url":"https://kudago.com/msk/event/kontsert-detskij-albom-chajkovskogo-muzyikalnoe-puteshestvie/","dates":[{"start":1790416800,"end":1790416800}],"place":{"id":745,"title":"Московский международный Дом музыки (ММДМ)","coords":{"lat":55.733249,"lon":37.646597}}}`

const secondEvent = `{"id":195994,"title":"детская творческая программа «Пикассо детям»","site_url":"https://kudago.com/msk/event/picasso/","dates":[{"start":1790503200,"end":1790510400}]}`

func newAdapter(url string) *Adapter {
	return New(url, &http.Client{Timeout: 5 * time.Second}, func() time.Time { return fixedNow })
}

func TestFetchWalksAllPages(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != ingest.UserAgent || r.URL.Path != eventsPath {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		switch q.Get("page") {
		case "":
			if q.Get("location") != "msk" || q.Get("actual_since") != "1790460000" || q.Get("expand") != "place,dates" {
				http.Error(w, "unexpected query", http.StatusBadRequest)
				return
			}
			next := server.URL + eventsPath + "?location=msk&page=2"
			w.Write([]byte(`{"count":3,"next":"` + next + `","previous":null,"results":[` + firstEvent + `,{"id":0,"title":"broken"}]}`))
		case "2":
			w.Write([]byte(`{"count":3,"next":null,"previous":"x","results":[` + secondEvent + `]}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	batch, err := newAdapter(server.URL).Fetch(context.Background(), domain.Moscow, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 2 || batch.Skipped != 1 {
		t.Fatalf("%d records, %d skipped", len(batch.Records), batch.Skipped)
	}
	first := batch.Records[0]
	if first.ExternalID != "event:226287" ||
		first.SourceURL != "https://kudago.com/msk/event/kontsert-detskij-albom-chajkovskogo-muzyikalnoe-puteshestvie/" ||
		string(first.Payload) != firstEvent || first.ContentType != "application/json" || first.SourceUpdatedAt != nil {
		t.Fatalf("first record = %+v", first)
	}
	if batch.Records[1].ExternalID != "event:195994" {
		t.Fatalf("second record = %s", batch.Records[1].ExternalID)
	}
	var cursor map[string]string
	if err := json.Unmarshal(batch.Cursor, &cursor); err != nil || cursor["snapshot_at"] != "2026-09-26T22:00:00Z" {
		t.Fatalf("cursor = %s", batch.Cursor)
	}
}

func TestFetchPermHasNoEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("KudaGo must not be called for Perm")
	}))
	defer server.Close()

	batch, err := newAdapter(server.URL).Fetch(context.Background(), domain.Perm, nil)
	if err != nil || len(batch.Records) != 0 || len(batch.Cursor) == 0 {
		t.Fatalf("batch = %+v, err = %v", batch, err)
	}
}

func TestFetchErrorCodes(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http_status_503": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"decode":          func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>busy</html>")) },
	}
	for code, handler := range cases {
		server := httptest.NewServer(handler)
		_, err := newAdapter(server.URL).Fetch(context.Background(), domain.Moscow, nil)
		server.Close()
		var fetchErr *ingest.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != code {
			t.Fatalf("want %s, got %v", code, err)
		}
		if strings.Contains(fetchErr.Code, "http://") {
			t.Fatal("error code leaks the URL")
		}
	}
}

func TestSourceIsLive(t *testing.T) {
	s := newAdapter("http://unused").Source()
	if s.Key != domain.KudaGo || s.DataMode != domain.Live || s.AccessMode != domain.AccessAPI {
		t.Fatalf("source = %+v", s)
	}
}
