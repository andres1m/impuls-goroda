package kudago

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

const (
	DefaultBaseURL = "https://kudago.com"
	eventsPath     = "/public-api/v1.4/events/"
	eventFields    = "id,publication_date,dates,title,short_title,slug,place,description,body_text,location,categories,tagline,age_restriction,price,is_free,site_url,tags"
	pageSize       = 100
	maxPages       = 100
)

type Adapter struct {
	baseURL string
	client  *http.Client
	now     func() time.Time
}

func New(baseURL string, client *http.Client, now func() time.Time) *Adapter {
	return &Adapter{baseURL: baseURL, client: client, now: now}
}

func (a *Adapter) Source() domain.Source {
	return domain.Source{
		Key:              domain.KudaGo,
		Name:             "KudaGo",
		DocumentationURL: "https://docs.kudago.com/api/",
		AccessMode:       domain.AccessAPI,
		LicenseInfo:      "Открытая лицензия KudaGo; каждая запись показывается с индексируемой ссылкой на свою страницу kudago.com",
		SchemaVersion:    "kudago-api-1.4",
		DataMode:         domain.Live,
	}
}

type page struct {
	Next    *string           `json:"next"`
	Results []json.RawMessage `json:"results"`
}

type item struct {
	ID      int64  `json:"id"`
	SiteURL string `json:"site_url"`
}

func (a *Adapter) Fetch(ctx context.Context, city domain.City, _ json.RawMessage) (ingest.Batch, error) {
	snapshotAt := a.now().UTC()
	cursor, err := json.Marshal(map[string]string{"snapshot_at": snapshotAt.Format(time.RFC3339)})
	if err != nil {
		return ingest.Batch{}, err
	}
	batch := ingest.Batch{Cursor: cursor}
	// KudaGo does not cover Perm.
	if city != domain.Moscow {
		return batch, nil
	}

	query := url.Values{
		"location":     {"msk"},
		"actual_since": {strconv.FormatInt(snapshotAt.Unix(), 10)},
		"expand":       {"place,dates"},
		"fields":       {eventFields},
		"page_size":    {strconv.Itoa(pageSize)},
	}
	next := a.baseURL + eventsPath + "?" + query.Encode()
	for pages := 0; next != ""; pages++ {
		if pages == maxPages {
			return ingest.Batch{}, &ingest.FetchError{Code: "too_many_pages", Err: fmt.Errorf("more than %d pages", maxPages)}
		}
		p, err := a.page(ctx, next)
		if err != nil {
			return ingest.Batch{}, err
		}
		for _, raw := range p.Results {
			var it item
			if err := json.Unmarshal(raw, &it); err != nil {
				return ingest.Batch{}, &ingest.FetchError{Code: "decode", Err: err}
			}
			if it.ID == 0 || it.SiteURL == "" {
				batch.Skipped++
				continue
			}
			batch.Records = append(batch.Records, domain.RawRecord{
				ExternalID:  "event:" + strconv.FormatInt(it.ID, 10),
				SourceURL:   it.SiteURL,
				Payload:     raw,
				ContentType: "application/json",
			})
		}
		next = ""
		if p.Next != nil {
			next = *p.Next
		}
	}
	return batch, nil
}

func (a *Adapter) page(ctx context.Context, pageURL string) (page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return page{}, &ingest.FetchError{Code: "request", Err: err}
	}
	req.Header.Set("User-Agent", ingest.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return page{}, &ingest.FetchError{Code: "transport", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return page{}, &ingest.FetchError{Code: fmt.Sprintf("http_status_%d", resp.StatusCode), Err: errors.New(resp.Status)}
	}
	var p page
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return page{}, &ingest.FetchError{Code: "decode", Err: err}
	}
	return p, nil
}
