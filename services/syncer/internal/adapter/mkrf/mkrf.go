package mkrf

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

//go:embed data/events.jsonl.gz
var eventsGzip []byte

const (
	datasetURL          = "https://opendata.mkrf.ru/opendata/7705851331-events"
	datasetVersion      = 12
	initialScanBufBytes = 1 << 20
	maxRecordBytes      = 16 << 20
	errCodeDecode       = "decode"
)

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) Source() domain.Source {
	return domain.Source{
		Key:              domain.MkrfEvents,
		Name:             "Минкультуры России: мероприятия в сфере культуры",
		DocumentationURL: datasetURL,
		AccessMode:       domain.AccessExport,
		LicenseInfo:      "Типовые условия использования открытых данных Российской Федерации; ссылка на источник обязательна",
		SchemaVersion:    "mkrf-events-12",
		DataMode:         domain.Prepared,
	}
}

func (Adapter) Fetch(ctx context.Context, city domain.City, _ json.RawMessage) (ingest.Batch, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(eventsGzip))
	if err != nil {
		return ingest.Batch{}, &ingest.FetchError{Code: errCodeDecode, Err: err}
	}
	defer unzipped.Close()

	scanner := bufio.NewScanner(unzipped)
	scanner.Buffer(make([]byte, 0, initialScanBufBytes), maxRecordBytes)
	batch := ingest.Batch{Cursor: json.RawMessage(`{"dataset_version":` + strconv.Itoa(datasetVersion) + `}`)}
	for scanner.Scan() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ingest.Batch{}, fmt.Errorf("mkrf scan context: %w", ctxErr)
		}
		line := scanner.Bytes()
		var ev event
		if unmarshalErr := json.Unmarshal(line, &ev); unmarshalErr != nil {
			return ingest.Batch{}, &ingest.FetchError{Code: errCodeDecode, Err: unmarshalErr}
		}
		if ev.city() != city {
			continue
		}
		if ev.Data.General.ID == 0 {
			batch.Skipped++
			continue
		}
		batch.Records = append(batch.Records, ev.rawRecord(bytes.Clone(line)))
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return ingest.Batch{}, &ingest.FetchError{Code: errCodeDecode, Err: scanErr}
	}
	return batch, nil
}

type event struct {
	Data struct {
		Info struct {
			UpdateDate string `json:"updateDate"`
		} `json:"info"`
		General struct {
			ID     int64 `json:"id"`
			Places []struct {
				Address struct {
					FullAddress string `json:"fullAddress"`
				} `json:"address"`
				Locale struct {
					Name string `json:"name"`
				} `json:"locale"`
			} `json:"places"`
		} `json:"general"`
	} `json:"data"`
}

func (e event) city() domain.City {
	for _, place := range e.Data.General.Places {
		switch {
		case strings.Contains(place.Address.FullAddress, "г Москва") || place.Locale.Name == "Москва":
			return domain.Moscow
		case strings.Contains(place.Address.FullAddress, "г Пермь") || place.Locale.Name == "Пермь":
			return domain.Perm
		}
	}
	return ""
}

func (e event) rawRecord(payload []byte) domain.RawRecord {
	record := domain.RawRecord{
		ExternalID:      "event:" + strconv.FormatInt(e.Data.General.ID, 10),
		SourceURL:       datasetURL,
		Payload:         payload,
		ContentType:     "application/json",
		ProviderVersion: strconv.Itoa(datasetVersion),
	}
	if updated, err := time.Parse(time.RFC3339, e.Data.Info.UpdateDate); err == nil {
		record.SourceUpdatedAt = &updated
	}
	return record
}
