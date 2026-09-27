package seed

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const horizonDays = 7

type Dataset struct {
	Version string  `yaml:"dataset_version"`
	City    string  `yaml:"city"`
	Places  []Place `yaml:"places"`
	Events  []Event `yaml:"events"`
}

type Place struct {
	Key          string        `yaml:"key"`
	Kind         string        `yaml:"kind"`
	Title        string        `yaml:"title"`
	Category     string        `yaml:"category"`
	Tags         []string      `yaml:"tags"`
	Lat          float64       `yaml:"lat"`
	Lon          float64       `yaml:"lon"`
	Address      string        `yaml:"address"`
	OpeningRules *OpeningRules `yaml:"opening_rules"`
}

type Event struct {
	Key       string    `yaml:"key"`
	Place     string    `yaml:"place"`
	Title     string    `yaml:"title"`
	Category  string    `yaml:"category"`
	Tags      []string  `yaml:"tags"`
	Organizer string    `yaml:"organizer"`
	AgeMin    *int      `yaml:"age_min"`
	Sessions  []Session `yaml:"sessions"`
}

type Session struct {
	Key                      string        `yaml:"key"`
	Days                     []int         `yaml:"days"`
	Slot                     string        `yaml:"slot"`
	Start                    string        `yaml:"start"`
	End                      string        `yaml:"end"`
	MinDuration              time.Duration `yaml:"min_duration"`
	RecommendedDuration      time.Duration `yaml:"recommended_duration"`
	Buffer                   time.Duration `yaml:"buffer"`
	LateEntry                *bool         `yaml:"late_entry"`
	LastEntry                string        `yaml:"last_entry"`
	Access                   string        `yaml:"access"`
	RegistrationClosesBefore time.Duration `yaml:"registration_closes_before"`
	Availability             string        `yaml:"availability"`
	CancellationReason       string        `yaml:"cancellation_reason"`
	Hard                     *bool         `yaml:"hard"`
	Prices                   []Price       `yaml:"prices"`
}

type Price struct {
	Audience        string   `yaml:"audience"`
	Status          string   `yaml:"status"`
	Amount          []int64  `yaml:"amount"`
	TariffLabel     string   `yaml:"tariff_label"`
	EligibilityAge  []int    `yaml:"eligibility_age"`
	BenefitPrograms []string `yaml:"benefit_programs"`
}

// Reference holds the shared dictionaries a dataset must agree with.
type Reference struct {
	Categories map[string]bool
	TagBits    map[string]int
}

var (
	keyPattern      = regexp.MustCompile(`^[a-z0-9-]+$`)
	slotTypes       = map[string]string{"fixed": "FIXED_SESSION", "window": "CONTINUOUS_WINDOW"}
	accessTypes     = setOf("free", "registration", "ticket")
	availabilities  = setOf("available", "registration_required", "sold_out", "cancelled", "unknown")
	audiences       = setOf("general", "child", "student", "senior", "other")
	benefitPrograms = setOf("pushkin_card")
)

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func Source(version string) domain.Source {
	return domain.Source{
		Key:           domain.SyntheticSource,
		Name:          "Синтетические данные",
		AccessMode:    domain.AccessSynthetic,
		SchemaVersion: version,
		DataMode:      domain.Synthetic,
	}
}

func parse(raw []byte) (Dataset, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var ds Dataset
	if err := decoder.Decode(&ds); err != nil {
		return Dataset{}, err
	}
	return ds, nil
}

func (ds Dataset) validate(ref Reference) error {
	if ds.Version == "" {
		return errors.New("dataset_version is required")
	}
	places := make(map[string]Place, len(ds.Places))
	for _, p := range ds.Places {
		if err := p.validate(ref); err != nil {
			return fmt.Errorf("place %q: %w", p.Key, err)
		}
		if _, duplicate := places[p.Key]; duplicate {
			return fmt.Errorf("place %q: duplicate key", p.Key)
		}
		places[p.Key] = p
	}
	events := make(map[string]bool, len(ds.Events))
	hosting := make(map[string]bool)
	for _, e := range ds.Events {
		if err := e.validate(ref, places); err != nil {
			return fmt.Errorf("event %q: %w", e.Key, err)
		}
		if events[e.Key] {
			return fmt.Errorf("event %q: duplicate key", e.Key)
		}
		events[e.Key] = true
		hosting[e.Place] = true
	}
	for _, p := range ds.Places {
		if p.Category == "" && !hosting[p.Key] {
			return fmt.Errorf("place %q: has neither a category nor events", p.Key)
		}
	}
	return nil
}

func (p Place) validate(ref Reference) error {
	if !keyPattern.MatchString(p.Key) {
		return errors.New("key must match [a-z0-9-]+")
	}
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("title is required")
	}
	if p.Category != "" && !ref.Categories[p.Category] {
		return fmt.Errorf("unknown category %q", p.Category)
	}
	if _, err := tagMask(p.Tags, ref); err != nil {
		return err
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return errors.New("coordinates are out of range")
	}
	if p.OpeningRules != nil {
		if err := p.OpeningRules.validate(); err != nil {
			return fmt.Errorf("opening_rules: %w", err)
		}
	}
	switch p.Kind {
	case "public_space":
		if p.Category == "" || p.OpeningRules == nil || !p.OpeningRules.hasOpenHours() {
			return errors.New("a public space needs a category and opening hours")
		}
	case "venue":
	default:
		return fmt.Errorf("unknown kind %q", p.Kind)
	}
	return nil
}

func (e Event) validate(ref Reference, places map[string]Place) error {
	if !keyPattern.MatchString(e.Key) {
		return errors.New("key must match [a-z0-9-]+")
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("title is required")
	}
	place, ok := places[e.Place]
	if !ok {
		return fmt.Errorf("unknown place %q", e.Place)
	}
	if place.Kind != "venue" {
		return errors.New("events are held only at venues")
	}
	if !ref.Categories[e.Category] {
		return fmt.Errorf("unknown category %q", e.Category)
	}
	if _, err := tagMask(e.Tags, ref); err != nil {
		return err
	}
	if e.AgeMin != nil && *e.AgeMin < 0 {
		return errors.New("age_min must not be negative")
	}
	if len(e.Sessions) == 0 {
		return errors.New("at least one session is required")
	}
	keys := make(map[string]bool, len(e.Sessions))
	for _, s := range e.Sessions {
		if err := s.validate(); err != nil {
			return fmt.Errorf("session %q: %w", s.Key, err)
		}
		if keys[s.Key] {
			return fmt.Errorf("session %q: duplicate key", s.Key)
		}
		keys[s.Key] = true
	}
	return nil
}

func (s Session) validate() error {
	if !keyPattern.MatchString(s.Key) {
		return errors.New("key must match [a-z0-9-]+")
	}
	if _, ok := slotTypes[s.Slot]; !ok {
		return fmt.Errorf("unknown slot %q", s.Slot)
	}
	days := make(map[int]bool, len(s.Days))
	for _, day := range s.Days {
		if day < 0 || day >= horizonDays || days[day] {
			return fmt.Errorf("day offset %d is repeated or outside 0..%d", day, horizonDays-1)
		}
		days[day] = true
	}
	start, err := clockMinutes(s.Start, false)
	if err != nil {
		return err
	}
	end, err := clockMinutes(s.End, true)
	if err != nil {
		return err
	}
	if start >= end {
		return errors.New("start must be before end")
	}
	if s.MinDuration <= 0 || s.MinDuration > time.Duration(end-start)*time.Minute {
		return errors.New("min_duration must be positive and fit the session")
	}
	if s.RecommendedDuration != 0 && s.RecommendedDuration < s.MinDuration {
		return errors.New("recommended_duration must not be shorter than min_duration")
	}
	if s.Buffer < 0 {
		return errors.New("buffer must not be negative")
	}
	if s.LastEntry != "" {
		if s.LateEntry == nil || !*s.LateEntry {
			return errors.New("last_entry requires late_entry: true")
		}
		last, err := clockMinutes(s.LastEntry, true)
		if err != nil {
			return err
		}
		if last < start || last > end {
			return errors.New("last_entry is outside the session")
		}
	}
	if !accessTypes[s.Access] {
		return fmt.Errorf("unknown access %q", s.Access)
	}
	if s.RegistrationClosesBefore < 0 || (s.RegistrationClosesBefore > 0 && s.Access != "registration") {
		return errors.New("registration_closes_before applies only to registration access")
	}
	if !availabilities[s.Availability] {
		return fmt.Errorf("unknown availability %q", s.Availability)
	}
	if (s.Availability == "cancelled") != (s.CancellationReason != "") {
		return errors.New("cancellation_reason is required exactly for cancelled sessions")
	}
	if s.Access == "ticket" && len(s.Prices) == 0 {
		return errors.New("a ticketed session needs prices")
	}
	if s.Access != "ticket" && len(s.Prices) > 0 {
		return errors.New("only ticketed sessions list prices")
	}
	seen := make(map[string]bool, len(s.Prices))
	for _, p := range s.Prices {
		if err := p.validate(); err != nil {
			return fmt.Errorf("price %q: %w", p.Audience, err)
		}
		if seen[p.Audience] {
			return fmt.Errorf("price %q: duplicate audience", p.Audience)
		}
		seen[p.Audience] = true
	}
	return nil
}

func (p Price) validate() error {
	if !audiences[p.Audience] {
		return fmt.Errorf("unknown audience %q", p.Audience)
	}
	amounts := map[string]int{"free": 0, "unknown": 0, "fixed": 1, "range": 2}
	count, ok := amounts[p.Status]
	if !ok {
		return fmt.Errorf("unknown status %q", p.Status)
	}
	if len(p.Amount) != count {
		return fmt.Errorf("a %s price has %d amounts", p.Status, count)
	}
	for _, amount := range p.Amount {
		if amount < 0 {
			return errors.New("amount must not be negative")
		}
	}
	if p.Status == "range" && p.Amount[0] > p.Amount[1] {
		return errors.New("range is inverted")
	}
	if len(p.EligibilityAge) != 0 && (len(p.EligibilityAge) != 2 || p.EligibilityAge[0] < 0 || p.EligibilityAge[0] > p.EligibilityAge[1]) {
		return errors.New("eligibility_age must be [min, max]")
	}
	for _, program := range p.BenefitPrograms {
		if !benefitPrograms[program] {
			return fmt.Errorf("unknown benefit program %q", program)
		}
	}
	return nil
}

func tagMask(tags []string, ref Reference) (int64, error) {
	var mask int64
	for _, tag := range tags {
		bit, ok := ref.TagBits[tag]
		if !ok {
			return 0, fmt.Errorf("unknown interest tag %q", tag)
		}
		mask |= 1 << bit
	}
	return mask, nil
}
