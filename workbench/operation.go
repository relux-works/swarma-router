package workbench

import (
	"context"
	"encoding/json"
	"time"

	"relux.works/swarma-router/launch"
)

const OperationContract = "swarma-router-operation/0.2.0-draft.2"
const MaxResponseBytes = 131072

// Repository is admitted out of band. Open serializes this exact tuple across
// all cooperating writers until Close. No alternate store or discovery fallback
// is selected by a request. A missing record must be distinguished from an error.
type Repository interface {
	Open(context.Context, OperationKey) (Journal, error)
}

// Journal owns actual storage custody, atomic expected-revision comparison and
// durability. Read returns nil,nil ONLY for reliable absence. Commit calls build
// exactly once with a new nonempty opaque revision while holding its transaction,
// compares expected (empty means absent), and returns nil only after those exact
// bytes and revision are durable. An error may mean committed-but-unacknowledged.
// Close releases custody; it does not retract a commit. All methods honor ctx.
// This package provides no disk implementation or claim of production durability.
type Journal interface {
	Read(context.Context) ([]byte, error)
	Commit(context.Context, string, func(nextRevision string) ([]byte, error)) error
	Close() error
}

// Access is current trusted local authorization, never request JSON. Read may
// check storage access only; matching Prepare replay performs no task/inventory/
// qualification or launch source acquisition. Prepare/Observe check their own
// attenuated permission, separately from the full source checks below.
type Access interface {
	Read(context.Context, OperationKey) error
	Prepare(context.Context, OperationKey) error
	Observe(context.Context, OperationKey) error
}

// Refusal carries one closed reason from the agreed vocabulary. Unknown errors
// become a conservative boundary-specific reason; error text is never returned.
type Refusal struct{ Reason string }

func (r Refusal) Error() string { return r.Reason }

// Snapshot contains complete sanitized normalized initial source/check content
// supplied by the trusted planner, not raw private State, credentials or session
// payloads. Bytes preserve that content exactly; refs and SHA256 bind it, without
// adding source authentication or freshness. It is retained, never wire-echoed.
type Snapshot struct {
	Data       []byte        `json:"data"`
	Digest     string        `json:"digest"`
	References []RevisionRef `json:"references"`
}

type SelectedBinding struct {
	Member        string        `json:"member"`
	Card          RevisionRef   `json:"card"`
	Configuration Configuration `json:"configuration"`
	Proposal      RevisionRef   `json:"proposal"`
}

type LowerBinding struct {
	Plugin       string `json:"plugin"`
	OperationRef string `json:"operation_ref"`
	IntentDigest string `json:"intent_digest"`
}

// LowerMapping retains the exact existing lower digest algorithm and complete
// intent, separately from the v2 canonical digest and artifact mapping receipt.
type LowerMapping struct {
	Binding LowerBinding  `json:"binding"`
	Intent  launch.Intent `json:"intent"`
	Mapping RevisionRef   `json:"mapping"`
}

type ReadyBinding struct {
	Selected        SelectedBinding   `json:"selected"`
	ExecutionOwner  string            `json:"execution_owner"`
	ManagerCustody  RevisionRef       `json:"manager_custody"`
	TaskState       RevisionRef       `json:"task_state"`
	RuntimeEvidence RevisionRef       `json:"runtime_evidence"`
	MessagingRoute  RevisionRef       `json:"messaging_route"`
	LaneReply       RevisionRef       `json:"lane_reply"`
	Execution       map[string]string `json:"execution"`
	ValidUntil      string            `json:"valid_until"`
}

// Observation comes only from an admitted same-operation owner witness. Its
// adapter checks actual runtime/configuration/lock/custody/task/route/lane and
// separate native/messaging identities. Lower running alone is insufficient.
// Empty times are allowed only for unknown, never a positive pending/ready claim.
type Observation struct {
	Key           OperationKey    `json:"key"`
	RequestDigest string          `json:"request_digest"`
	Selected      SelectedBinding `json:"selected"`
	Lower         *LowerBinding   `json:"lower"`
	Status        string          `json:"status"`
	Reason        string          `json:"reason"`
	CheckedAt     string          `json:"checked_at"`
	ValidUntil    string          `json:"valid_until"`
	Ready         *ReadyBinding   `json:"ready"`
	Evidence      []RevisionRef   `json:"evidence"`
}

type Plan struct {
	Sources        *Snapshot
	Proposal       *RevisionRef
	Selected       *SelectedBinding
	Action         string // reuse/start, or empty for a terminal no-effect outcome.
	Status, Reason string // selected/ok, or one terminal no-effect status/reason.
}

// Planner resolves once during the original new Prepare and normalizes actual
// admitted sources before deterministic selection. Revalidate checks current
// full revisions, holds, authority, custody, task state and action prerequisites
// immediately before the next effect/observation. It never changes the proposal.
type Planner interface {
	Resolve(context.Context, PrepareRequest) (Plan, error)
	Revalidate(context.Context, View) error
}

// Lifecycle uses the existing admitted lower journal/intent, not a new target or
// retry route. Prepare is PURE mapping/command preparation. Start calls that
// lower operation at most once here; lower Guard still rechecks admission.
// Observe uses only the retained lower operation or retained reuse target. C1's
// internal pure retained-command helper and C2's same-record writes are allowed;
// new selection, lifecycle Prepare/Start, assignment or messages are forbidden.
// Every method is synchronous, honors ctx and must not retain mutable arguments.
type Lifecycle interface {
	Prepare(context.Context, View) (LowerMapping, error)
	Start(context.Context, View) (Observation, error)
	Observe(context.Context, View) (Observation, error)
}

// View exposes copied exact bindings to trusted adapters, with no write methods.
type View struct {
	record  operationRecord
	binding RetainedRequest
}

func (v View) Key() OperationKey                 { return v.binding.Key() }
func (v View) Digest() string                    { return v.binding.Digest() }
func (v View) Request() PrepareRequest           { p, _ := v.binding.Prepare(); return p }
func (v View) Selected() *SelectedBinding        { return clone(v.record.Selected) }
func (v View) Lower() *LowerMapping              { return clone(v.record.Lower) }
func (v View) KnownExecution() map[string]string { return clone(v.record.KnownExecution) }
func (v View) Sources() *Snapshot                { return clone(v.record.Sources) }

type Receipt struct {
	Kind            string           `json:"kind"`
	OperationRef    string           `json:"operation_ref"`
	DurableRevision string           `json:"durable_revision"`
	Status          string           `json:"status"`
	Reason          string           `json:"reason"`
	Historical      bool             `json:"historical"`
	Selected        *SelectedBinding `json:"selected"`
	Lower           *LowerBinding    `json:"lower_launch"`
	Ready           *ReadyBinding    `json:"ready"`
	Evidence        []RevisionRef    `json:"evidence"`
}

type operationRecord struct {
	Contract         string            `json:"contract"`
	Request          json.RawMessage   `json:"request"`
	Phase            string            `json:"phase"`
	Sources          *Snapshot         `json:"initial_sources"`
	Proposal         *RevisionRef      `json:"proposal"`
	Selected         *SelectedBinding  `json:"selected_binding"`
	Lower            *LowerMapping     `json:"lower_launch_binding"`
	Action           string            `json:"action"`
	PossibleEffect   bool              `json:"possible_effect"`
	TerminalNoEffect bool              `json:"terminal_no_effect"`
	KnownExecution   map[string]string `json:"known_execution"`
	LastObservation  *Observation      `json:"last_observation"`
	Receipt          *Receipt          `json:"receipt"`
}

type Engine struct {
	Repository Repository
	Access     Access
	Planner    Planner
	Lifecycle  Lifecycle
	Clock      func() time.Time
}

// Output owns its immutable complete JSON+LF frame and process-exit convention.
// Only a durable fresh ready receipt has exit zero; no assignment is implied.
type Output struct {
	data []byte
	exit int
}

func (o Output) Bytes() []byte { return append([]byte(nil), o.data...) }
func (o Output) ExitCode() int { return o.exit }

func clone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic("invalid internal clone")
	}
	var out T
	if json.Unmarshal(b, &out) != nil {
		panic("invalid internal clone")
	}
	return out
}
