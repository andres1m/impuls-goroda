package domain

import (
	"fmt"
	"time"
)

type City string

const (
	Moscow City = "moscow"
	Perm   City = "perm"
)

func ParseCity(code string) (City, error) {
	switch city := City(code); city {
	case Moscow, Perm:
		return city, nil
	}
	return "", fmt.Errorf("unknown city %q", code)
}

type SourceKey string

const (
	MkrfEvents      SourceKey = "mkrf_events"
	KudaGo          SourceKey = "kudago"
	OSM             SourceKey = "osm"
	SyntheticSource SourceKey = "synthetic"
)

type DataMode string

const (
	Live      DataMode = "live"
	Prepared  DataMode = "prepared"
	Synthetic DataMode = "synthetic"
)

type AccessMode string

const (
	AccessAPI       AccessMode = "api"
	AccessExport    AccessMode = "export"
	AccessSynthetic AccessMode = "synthetic"
)

type Source struct {
	Key              SourceKey
	Name             string
	DocumentationURL string
	AccessMode       AccessMode
	LicenseInfo      string
	SchemaVersion    string
	DataMode         DataMode
}

type RawRecord struct {
	ExternalID      string
	SourceURL       string
	Payload         []byte
	ContentType     string
	SourceUpdatedAt *time.Time
	ProviderVersion string
}
