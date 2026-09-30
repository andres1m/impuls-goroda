package routewire

import (
	"time"
)

type ConfirmedRouteInput struct {
	City        string           `json:"city"`
	Constraints RouteConstraints `json:"constraints"`
	Destination *Coordinate      `json:"destination,omitempty"`
	EndAt       time.Time        `json:"end_at"`
	Origin      Coordinate       `json:"origin"`
	StartAt     time.Time        `json:"start_at"`
	Timezone    string           `json:"timezone"`
}
type OptimizeResponse struct {
	ComputationTimeMS int64        `json:"computation_time_ms"`
	Conflicts         []Conflict   `json:"conflicts"`
	DataAsOf          *time.Time   `json:"data_as_of,omitempty"`
	DataMode          string       `json:"data_mode"`
	RequestID         string       `json:"request_id"`
	Routes            []OwnerRoute `json:"routes"`
	Status            string       `json:"status"`
	Warnings          []Warning    `json:"warnings"`
}
type OwnerRoute struct {
	PendingProposal *PendingProposal     `json:"pending_proposal,omitempty"`
	City            string               `json:"city"`
	CreatedAt       time.Time            `json:"created_at"`
	Execution       []OwnerExecution     `json:"execution"`
	Issues          []RouteIssue         `json:"issues"`
	Lifecycle       string               `json:"lifecycle"`
	Participation   []OwnerParticipation `json:"participation"`
	Plan            RoutePlan            `json:"plan"`
	Revision        string               `json:"revision"`
	RouteID         string               `json:"route_id"`
	UpdatedAt       time.Time            `json:"updated_at"`
}
type Conflict struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	VisitIds   []string `json:"visit_ids"`
	SessionIDs []string `json:"session_ids,omitempty"`
}
type Coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
type OwnerExecution struct {
	ActualEndedAt     *time.Time `json:"actual_ended_at,omitempty"`
	ActualStartedAt   *time.Time `json:"actual_started_at,omitempty"`
	ConfirmationKind  string     `json:"confirmation_kind"`
	Status            string     `json:"status"`
	UpdatedAt         time.Time  `json:"updated_at"`
	UpdatedInRevision string     `json:"updated_in_revision"`
	VisitID           string     `json:"visit_id"`
}
type OwnerParticipation struct {
	Evidence             string     `json:"evidence"`
	ExternalLinkOpenedAt *time.Time `json:"external_link_opened_at,omitempty"`
	PrivateReference     *string    `json:"private_reference,omitempty"`
	Status               string     `json:"status"`
	UpdatedAt            time.Time  `json:"updated_at"`
	UpdatedInRevision    string     `json:"updated_in_revision"`
	VisitID              string     `json:"visit_id"`
}
type RouteConstraints struct {
	SemanticQuery      *string           `json:"semantic_query,omitempty"`
	AcceptedUnknowns   []string          `json:"accepted_unknowns"`
	AudienceClaims     []AudienceClaim   `json:"audience_claims"`
	BenefitPrograms    []string          `json:"benefit_programs"`
	Budget             Budget            `json:"budget"`
	ExcludedCategories []string          `json:"excluded_categories"`
	InterestMask       string            `json:"interest_mask"`
	LoadProfile        string            `json:"load_profile"`
	LunchWindow        *LunchWindow      `json:"lunch_window,omitempty"`
	MovementModes      []string          `json:"movement_modes"`
	Obligations        []RouteObligation `json:"obligations"`
	PushkinCardOnly    *bool             `json:"pushkin_card_only,omitempty"`
	SoftPreferences    []string          `json:"soft_preferences"`
}
type RouteIssue struct {
	CatalogRevision *string    `json:"catalog_revision,omitempty"`
	Code            string     `json:"code"`
	CreatedAt       time.Time  `json:"created_at"`
	IssueID         string     `json:"issue_id"`
	Message         string     `json:"message"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	State           string     `json:"state"`
	Type            string     `json:"type"`
	VisitID         *string    `json:"visit_id,omitempty"`
}
type RoutePlan struct {
	ArchetypeID     string           `json:"archetype_id"`
	CatalogRevision string           `json:"catalog_revision"`
	Conflicts       []Conflict       `json:"conflicts"`
	Constraints     RouteConstraints `json:"constraints"`
	Cost            CostSummary      `json:"cost"`
	Destination     *Coordinate      `json:"destination,omitempty"`
	EndAt           time.Time        `json:"end_at"`
	Geometry        []Coordinate     `json:"geometry"`
	Legs            []RouteLeg       `json:"legs"`
	Lifecycle       string           `json:"lifecycle"`
	Origin          Coordinate       `json:"origin"`
	Result          string           `json:"result"`
	SchemaVersion   int64            `json:"schema_version"`
	StartAt         time.Time        `json:"start_at"`
	Steps           []RouteStep      `json:"steps"`
	Timezone        string           `json:"timezone"`
	Warnings        []Warning        `json:"warnings"`
}
type Warning struct {
	Code        string  `json:"code"`
	LegPosition *int64  `json:"leg_position,omitempty"`
	Message     string  `json:"message"`
	Scope       string  `json:"scope"`
	VisitID     *string `json:"visit_id,omitempty"`
}
type AudienceClaim struct {
	Audience string `json:"audience"`
	Evidence string `json:"evidence"`
}
type Budget struct {
	Limit *Money `json:"limit,omitempty"`
	Mode  string `json:"mode"`
}
type CostSummary struct {
	BudgetConclusion  string                 `json:"budget_conclusion"`
	KnownPersonal     Money                  `json:"known_personal"`
	KnownTransport    Money                  `json:"known_transport"`
	ProgramAmount     Money                  `json:"program_amount"`
	TotalLower        *Money                 `json:"total_lower,omitempty"`
	TotalUpper        *Money                 `json:"total_upper,omitempty"`
	UnknownComponents []UnknownCostComponent `json:"unknown_components"`
}
type LunchWindow struct {
	EndAt              time.Time `json:"end_at"`
	MinDurationSeconds int64     `json:"min_duration_seconds"`
	StartAt            time.Time `json:"start_at"`
}
type RouteLeg struct {
	ArrivalAt      time.Time    `json:"arrival_at"`
	Cost           CostSnapshot `json:"cost"`
	DepartureAt    time.Time    `json:"departure_at"`
	DistanceMeters *float64     `json:"distance_meters,omitempty"`
	Evidence       LegEvidence  `json:"evidence"`
	FromKind       string       `json:"from_kind"`
	FromVisitID    *string      `json:"from_visit_id,omitempty"`
	Geometry       []Coordinate `json:"geometry,omitempty"`
	Mode           string       `json:"mode"`
	Position       int64        `json:"position"`
	ToKind         string       `json:"to_kind"`
	ToVisitID      *string      `json:"to_visit_id,omitempty"`
	Verification   string       `json:"verification"`
}
type RouteObligation struct {
	ArrivalBufferSeconds int64      `json:"arrival_buffer_seconds"`
	Participation        string     `json:"participation"`
	SessionID            *string    `json:"session_id,omitempty"`
	StartsAt             *time.Time `json:"starts_at,omitempty"`
	VisitID              *string    `json:"visit_id,omitempty"`
}
type RouteStep struct {
	AppliedConstraints []AppliedConstraint   `json:"applied_constraints"`
	ArrivalAt          time.Time             `json:"arrival_at"`
	Catalog            *CatalogSnapshot      `json:"catalog,omitempty"`
	Cost               *CostSnapshot         `json:"cost,omitempty"`
	Lunch              *LunchMetadata        `json:"lunch,omitempty"`
	ExternalVenue      *ExternalLunchVenue   `json:"external_venue,omitempty"`
	DepartureAt        time.Time             `json:"departure_at"`
	Kind               string                `json:"kind"`
	MinDurationSeconds int64                 `json:"min_duration_seconds"`
	Obligation         bool                  `json:"obligation"`
	Participation      ParticipationSnapshot `json:"participation"`
	Pinned             bool                  `json:"pinned"`
	Position           int64                 `json:"position"`
	VisitEndAt         time.Time             `json:"visit_end_at"`
	VisitID            string                `json:"visit_id"`
	VisitStartAt       time.Time             `json:"visit_start_at"`
}
type LunchMetadata struct {
	AfterVisitID    string `json:"after_visit_id"`
	DurationSeconds int64  `json:"duration_seconds"`
}
type ExternalLunchVenue struct {
	Provider          string     `json:"provider"`
	ExternalID        string     `json:"external_id"`
	Title             string     `json:"title"`
	Address           string     `json:"address,omitempty"`
	Position          Coordinate `json:"position"`
	ObservedAt        time.Time  `json:"observed_at"`
	Price             Price      `json:"price"`
	Availability      string     `json:"availability"`
	HoursVerification string     `json:"hours_verification"`
}
type AppliedConstraint struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Outcome  string `json:"outcome"`
	Strength string `json:"strength"`
}
type CatalogSnapshot struct {
	AgeRequirements     *string          `json:"age_requirements,omitempty"`
	Availability        string           `json:"availability"`
	Category            *string          `json:"category,omitempty"`
	DataMode            string           `json:"data_mode"`
	EventID             *string          `json:"event_id,omitempty"`
	InterestMask        string           `json:"interest_mask"`
	PlaceID             *string          `json:"place_id,omitempty"`
	Provenance          PublicProvenance `json:"provenance"`
	RegistrationDetails *string          `json:"registration_details,omitempty"`
	SessionEndsAt       *time.Time       `json:"session_ends_at,omitempty"`
	SessionID           *string          `json:"session_id,omitempty"`
	SessionStartsAt     *time.Time       `json:"session_starts_at,omitempty"`
	SessionVersion      *string          `json:"session_version,omitempty"`
	Title               string           `json:"title"`
}
type CostSnapshot struct {
	Audience          *string                `json:"audience,omitempty"`
	PersonalAmount    *Money                 `json:"personal_amount,omitempty"`
	Price             Price                  `json:"price"`
	PriceOfferID      *string                `json:"price_offer_id,omitempty"`
	ProgramAmount     *Money                 `json:"program_amount,omitempty"`
	Provenance        PublicProvenance       `json:"provenance"`
	UnknownComponents []UnknownCostComponent `json:"unknown_components"`
}
type LegEvidence struct {
	Limitations []string  `json:"limitations"`
	Method      string    `json:"method"`
	Mode        string    `json:"mode"`
	ObservedAt  time.Time `json:"observed_at"`
	Provider    string    `json:"provider"`
}
type Money struct {
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
}
type ParticipationSnapshot struct {
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
}
type UnknownCostComponent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Price struct {
	Currency   string  `json:"currency"`
	LowerMinor *string `json:"lower_minor,omitempty"`
	Status     string  `json:"status"`
	UpperMinor *string `json:"upper_minor,omitempty"`
}
type PublicProvenance struct {
	FetchedAt       time.Time  `json:"fetched_at"`
	SourceName      string     `json:"source_name"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
	SourceUrl       *string    `json:"source_url,omitempty"`
	VerifiedAt      *time.Time `json:"verified_at,omitempty"`
}
