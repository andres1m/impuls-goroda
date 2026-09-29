package osm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

const (
	DefaultBaseURL  = "https://overpass-api.de"
	interpreterPath = "/api/interpreter"
)

var cityAreas = map[domain.City]string{
	domain.Moscow: `area["name"="Москва"]["admin_level"="4"]`,
	domain.Perm:   `area["name"="Пермский городской округ"]["boundary"="administrative"]`,
}

// query asks for tags and centres only: metadata output would carry OSM contributors'
// names and ids, which the catalog must not store.
func query(city domain.City) string {
	return "[out:json][timeout:180];" + cityAreas[city] + "->.city;(" +
		`nwr(area.city)["tourism"~"^(museum|gallery|zoo|theme_park)$"]["name"];` +
		`nwr(area.city)["amenity"~"^(theatre|cinema|arts_centre)$"]["name"];` +
		`nwr(area.city)["leisure"~"^(park|garden|sports_centre|stadium)$"]["name"];` +
		`nwr(area.city)["amenity"~"^(cafe|restaurant|fast_food|food_court)$"]["name"];` +
		");out tags center;"
}

// Overpass often answers a heavy query with a timeout or an HTML error page; a moment later it works.
const (
	firstRetryDelay  = 10 * time.Second
	secondRetryDelay = 30 * time.Second
)

type Adapter struct {
	baseURL     string
	client      *http.Client
	now         func() time.Time
	retryDelays []time.Duration
}

func New(baseURL string, client *http.Client, now func() time.Time) *Adapter {
	return &Adapter{baseURL: baseURL, client: client, now: now,
		retryDelays: []time.Duration{firstRetryDelay, secondRetryDelay}}
}

// post sends an Overpass query and decodes the JSON answer into out.
func (a *Adapter) post(ctx context.Context, q string, out any) error {
	form := url.Values{"data": {q}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+interpreterPath,
		strings.NewReader(form.Encode()))
	if err != nil {
		return &ingest.FetchError{Code: "request", Err: err}
	}
	req.Header.Set("User-Agent", ingest.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return &ingest.FetchError{Code: "transport", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ingest.FetchError{Code: fmt.Sprintf("http_status_%d", resp.StatusCode), Err: errors.New(resp.Status)}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &ingest.FetchError{Code: "decode", Err: err}
	}
	return nil
}

func (a *Adapter) Source() domain.Source {
	return domain.Source{
		Key:              domain.OSM,
		Name:             "OpenStreetMap (Overpass API)",
		DocumentationURL: "https://wiki.openstreetmap.org/wiki/Overpass_API",
		AccessMode:       domain.AccessAPI,
		LicenseInfo:      "ODbL; attribution «© OpenStreetMap contributors» is required",
		SchemaVersion:    "overpass-leisure-places-2",
		DataMode:         domain.Live,
	}
}

type response struct {
	OSM3S struct {
		TimestampOSMBase string `json:"timestamp_osm_base"`
	} `json:"osm3s"`
	Remark   string            `json:"remark"`
	Elements []json.RawMessage `json:"elements"`
}

type element struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

func (a *Adapter) Fetch(ctx context.Context, city domain.City, _ json.RawMessage) (ingest.Batch, error) {
	snapshotAt := a.now().UTC()
	var body response
	if err := a.post(ctx, query(city), &body); err != nil {
		return ingest.Batch{}, err
	}

	// A remark means the query failed on the server and the element list may be partial.
	if body.Remark != "" {
		return ingest.Batch{}, &ingest.FetchError{Code: "overpass_remark", Err: errors.New(body.Remark)}
	}

	cursor, err := json.Marshal(map[string]string{
		"snapshot_at": snapshotAt.Format(time.RFC3339),
		"osm_base":    body.OSM3S.TimestampOSMBase,
	})
	if err != nil {
		return ingest.Batch{}, err
	}
	batch := ingest.Batch{Cursor: cursor}
	for _, raw := range body.Elements {
		var el element
		if err := json.Unmarshal(raw, &el); err != nil {
			return ingest.Batch{}, &ingest.FetchError{Code: "decode", Err: err}
		}
		if el.Type == "" || el.ID == 0 {
			batch.Skipped++
			continue
		}
		batch.Records = append(batch.Records, domain.RawRecord{
			ExternalID:  fmt.Sprintf("%s/%d", el.Type, el.ID),
			SourceURL:   fmt.Sprintf("https://www.openstreetmap.org/%s/%d", el.Type, el.ID),
			Payload:     raw,
			ContentType: "application/json",
		})
	}
	return batch, nil
}
