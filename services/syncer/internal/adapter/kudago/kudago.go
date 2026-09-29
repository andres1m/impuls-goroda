package kudago

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

const (
	DefaultBaseURL = "https://kudago.com"
	eventsPath     = "/public-api/v1.4/events/"
	eventFields    = "id,publication_date,dates,title,short_title,slug,place," +
		"description,body_text,location,categories,tagline,age_restriction,price,is_free,site_url,tags"
	pageSize      = 100
	maxPages      = 100
	errCodeDecode = "decode"
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
			return ingest.Batch{}, &ingest.FetchError{
				Code: "too_many_pages",
				Err:  fmt.Errorf("more than %d pages", maxPages),
			}
		}
		p, pageErr := a.page(ctx, next)
		if pageErr != nil {
			return ingest.Batch{}, pageErr
		}
		if appendErr := appendPageResults(&batch, p.Results); appendErr != nil {
			return ingest.Batch{}, appendErr
		}
		next = ""
		if p.Next != nil {
			next = *p.Next
		}
	}
	return batch, nil
}

func appendPageResults(batch *ingest.Batch, results []json.RawMessage) error {
	for _, raw := range results {
		var it item
		if err := json.Unmarshal(raw, &it); err != nil {
			return &ingest.FetchError{Code: errCodeDecode, Err: err}
		}
		if it.ID == 0 || it.SiteURL == "" {
			batch.Skipped++
			continue
		}
		hash, err := contentHash(raw)
		if err != nil {
			return &ingest.FetchError{Code: errCodeDecode, Err: err}
		}
		batch.Records = append(batch.Records, domain.RawRecord{
			ExternalID:  "event:" + strconv.FormatInt(it.ID, 10),
			SourceURL:   it.SiteURL,
			Payload:     raw,
			ContentType: "application/json",
			ContentHash: hash,
		})
	}
	return nil
}

// contentHash ignores the order of tags: KudaGo returns them shuffled on every
// request, and hashing raw bytes would land an unchanged event again each time.
func contentHash(raw json.RawMessage) ([]byte, error) {
	var event map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return nil, err
	}
	if tags, ok := event["tags"].([]any); ok {
		slices.SortFunc(tags, func(a, b any) int { return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	}
	canonical, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}

func (a *Adapter) page(ctx context.Context, pageURL string) (page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
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
		return page{}, &ingest.FetchError{
			Code: fmt.Sprintf("http_status_%d", resp.StatusCode),
			Err:  errors.New(resp.Status),
		}
	}
	var p page
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return page{}, &ingest.FetchError{Code: errCodeDecode, Err: err}
	}
	return p, nil
}
