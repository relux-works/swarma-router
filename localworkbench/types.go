// Package localworkbench recommends an inert workbench from explicitly admitted
// local observations and caller statements. It has no effect or persistence ports.
package localworkbench

import (
	"context"
	"time"
)

const Contract = "router-local-owner-inputs/1"
const MaxRequestBytes = 65536
const MaxSnapshotBytes = 1024 * 1024

// Opaque references retain exact decoded bytes; MXIDs are always full identities.
type LocalContext struct{ Caller, Project string }
type Card struct {
	Member string `json:"member"`
	MXID   string `json:"mxid"`
	SHA256 string `json:"sha256"`
}
type Configuration struct {
	Member      string `json:"member"`
	MXID        string `json:"mxid"`
	Runtime     string `json:"runtime"`
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	Profile     string `json:"profile"`
	ProfileLock string `json:"profile_lock"`
	Placement   string `json:"placement"`
}
type LocalRequest struct {
	Caller         string          `json:"caller"`
	Project        string          `json:"project"`
	CorrelationID  string          `json:"correlation_id"`
	TaskID         string          `json:"task_id"`
	Cards          []Card          `json:"cards"`
	Configurations []Configuration `json:"configurations"`
	Objective      string          `json:"objective"`
}
type Execution struct {
	NativeID   string    `json:"native_id"`
	Thread     string    `json:"thread"`
	Birth      time.Time `json:"birth"`
	Generation string    `json:"generation"`
}

// Binding keeps execution separate from declared configuration. A stopped seat
// has no execution; live state requires an actual execution identity.
type Binding struct {
	Member        string        `json:"member"`
	MXID          string        `json:"mxid"`
	CardSHA256    string        `json:"card_sha256"`
	Seat          string        `json:"seat"`
	Configuration Configuration `json:"configuration"`
	State         string        `json:"state"`
	Execution     *Execution    `json:"execution"`
}
type Source struct {
	Kind          string     `json:"kind"`
	Locator       string     `json:"locator"`
	ContentSHA256 string     `json:"content_sha256"`
	ReadAt        time.Time  `json:"read_at"`
	ObservedAt    *time.Time `json:"observed_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
}
type Subject struct {
	Member     string `json:"member"`
	MXID       string `json:"mxid"`
	CardSHA256 string `json:"card_sha256"`
}
type Declaration struct {
	Role    string  `json:"role"`
	Project *string `json:"project"`
}
type Qualification struct {
	Record    string `json:"record"`
	SHA256    string `json:"sha256"`
	Qualified bool   `json:"qualified"`
}
type Quota struct {
	Available uint64 `json:"available"`
	Needed    uint64 `json:"needed"`
	Unit      string `json:"unit"`
	Scope     string `json:"scope"`
	Complete  bool   `json:"complete"`
}
type Membership struct {
	Room     string `json:"room"`
	MXID     string `json:"mxid"`
	Joined   bool   `json:"joined"`
	Complete bool   `json:"complete"`
}
type Response struct {
	Sender        string `json:"sender"`
	To            string `json:"to"`
	TaskID        string `json:"task_id"`
	RequestDigest string `json:"request_digest"`
	Accepted      bool   `json:"accepted"`
}
type Absence struct {
	Seat     string `json:"seat"`
	MXID     string `json:"mxid"`
	Scope    string `json:"scope"`
	Complete bool   `json:"complete"`
	Absent   bool   `json:"absent"`
}
type Capability struct {
	Route         string        `json:"route"`
	Configuration Configuration `json:"configuration"`
	Qualified     bool          `json:"qualified"`
}
type LiveConfiguration struct {
	Source        Source        `json:"source"`
	Configuration Configuration `json:"configuration"`
	Execution     Execution     `json:"execution"`
}

// Projection is a closed union selected by the gate. Exactly the fields in the
// gate's schema are present; an unrelated true boolean cannot satisfy a gate.
type Projection struct {
	Subject       Subject            `json:"subject"`
	Configuration *Configuration     `json:"configuration,omitempty"`
	TaskID        *string            `json:"task_id,omitempty"`
	Statement     *string            `json:"statement,omitempty"`
	Declaration   *Declaration       `json:"declaration,omitempty"`
	Qualification *Qualification     `json:"qualification,omitempty"`
	Quota         *Quota             `json:"quota,omitempty"`
	Membership    *Membership        `json:"membership,omitempty"`
	Execution     *Execution         `json:"execution,omitempty"`
	Response      *Response          `json:"response,omitempty"`
	Available     *bool              `json:"available,omitempty"`
	Absence       *Absence           `json:"absence,omitempty"`
	Capability    *Capability        `json:"capability,omitempty"`
	Live          *LiveConfiguration `json:"live,omitempty"`
}

// Evidence is a closed, shallow union. Deny has one positive-shaped basis which
// must actually establish a false predicate, or an explicit caller denial.
type Evidence struct {
	Kind       string      `json:"kind"`
	Source     *Source     `json:"source,omitempty"`
	Projection *Projection `json:"projection,omitempty"`
	AssertedBy string      `json:"asserted_by,omitempty"`
	Statement  string      `json:"statement,omitempty"`
	RecordedAt *time.Time  `json:"recorded_at,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Basis      *Evidence   `json:"basis,omitempty"`
}
type GateRecord struct {
	Gate          string   `json:"gate"`
	RequestDigest string   `json:"request_digest"`
	TargetDigest  string   `json:"target_digest"`
	Evidence      Evidence `json:"evidence"`
}
type Target struct {
	Binding Binding      `json:"binding"`
	Checks  []GateRecord `json:"checks"`
}
type LocalOwnerInputs struct {
	Contract      string   `json:"contract"`
	RequestDigest string   `json:"request_digest"`
	Scope         string   `json:"scope"`
	Targets       []Target `json:"targets"`
}
type LocalSelection struct {
	RequestDigest string  `json:"request_digest"`
	Target        Binding `json:"target"`
}

// LocalOwnerReader is trusted in-process wiring. Its implementations must acquire
// the actual allowed sources and verify projections; decoding this type does not
// authenticate a source. It finishes synchronously and retains no mutable input.
// only narrows a refresh to the exact retained target, never new discovery.
type LocalOwnerReader interface {
	Read(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error)
}
type LocalPlanner struct {
	Local  LocalContext
	Room   string
	Reader LocalOwnerReader
	Clock  func() time.Time
}
type DeliveryDraft struct {
	Room      string `json:"room"`
	Recipient string `json:"recipient"`
	Text      string `json:"text"`
}
type PreparedWorkbench struct {
	Caller               string        `json:"caller"`
	Project              string        `json:"project"`
	TaskID               string        `json:"task_id"`
	CorrelationID        string        `json:"correlation_id"`
	Member               string        `json:"member"`
	MXID                 string        `json:"mxid"`
	Configuration        Configuration `json:"configuration"`
	RecommendationDigest string        `json:"recommendation_digest"`
	DeliveryDraft        DeliveryDraft `json:"delivery_draft"`
}
type LocalRecommendation struct {
	Contract           string             `json:"contract"`
	RequestDigest      string             `json:"request_digest"`
	AsOf               time.Time          `json:"as_of"`
	Scope              string             `json:"scope"`
	Action             string             `json:"action"`
	Selected           *Binding           `json:"selected"`
	Reason             string             `json:"reason"`
	Evidence           []GateRecord       `json:"evidence"`
	PreparedWorkbench  *PreparedWorkbench `json:"prepared_workbench"`
	ExecutionAuthority bool               `json:"execution_authority"`
}
