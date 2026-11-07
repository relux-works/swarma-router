# Workbench source planner

`workbench.SourcePlanner` implements the operation engine's `Planner` port. It
connects the bounded inventory consumer and deterministic selector to the full
Prepare identity. It performs no launch, stop, task assignment or message post.

The embedding supplies `Local`, `Owner` and `Clock` out of band. `OwnerReader`
must acquire and verify actual admitted task, card, policy, custody, mapping and
qualification receipts. The structs are a trusted Go port, not an authenticated
wire format or permission to read private state. There is no production owner
reader, Host command adapter or new external source protocol in this component.
Request JSON cannot supply an executable, callback, file, positive gate or source
selector. An absent source remains unavailable; no illustrative JSON is used as
production input. Tests use explicitly isolated source/lifecycle fixtures.

## Full identity and source evidence

An owner target retains the requested member/card/configuration, distinct native
member/project/workbench/model/profile, immutable mapping reference, actual owner
state and known execution. A ready target requires a complete a2a or Host
execution union; Host native identities remain distinct from messaging fields.
A stopped target cannot carry a current execution. Multiple stopped configuration
options are allowed. Contradictory full live configurations or executions for
one owner member are refused, even if native model labels happen to match.

Each owner witness binds the original key, full canonical request digest and
complete target digest, with its full receipt reference and actual source
observation/expiry. Zero witnesses and unknown times do not qualify a target.
Required gate names and verdicts are closed. Expired, future or mismatched allow
witnesses become unknown for selection; no collection-clock lifetime is added.
Missing or denied relevant checks exclude the action. Unneeded checks for the
other action do not introduce extra requirements.

Full task/card/policy/model/profile revision references remain in the retained
snapshot. Internal revision tokens hash the complete canonical reference, not
only its revision string or digest. Candidate IDs bind full requested artifacts,
native mapping and execution. Distinct artifact versions cannot collapse because
they use the same native name. Semantic input/target/check permutations normalize
deterministically. Hashes bind data; they do not authenticate sources or callers.

Optional Host data is read only through `inventory.Read` with its admitted exact
binding. Complete facts, provenance, coverage and source times are retained.
Configured values and last-thread-start confirmation remain their actual narrow
facts. Unknown effective effort/readiness/messaging remain unknown. Host activity,
idle state, missing rows or partial coverage never create stopped/ready evidence.
Failed/conflicting requested Host data cannot supply an action gate. A known
configuration contradiction or known true `security_stopped` / `stop_requested`
fact excludes reuse. False or unknown stop facts do not establish readiness;
separate qualified owner evidence remains necessary. An unrelated historical
configured value or stop-intent fact does not by itself override separately
established stopped absence.

When the target has a known Host execution, the admitted Host binding must name
that exact `execution` and `runner_instance`; a mismatch refuses before reading
the source. The a2a execution union has no comparable Host-native identifiers.
An a2a-ready target with requested Host data therefore remains unknown unless
the data already conflicts; no native-to-messaging identity link is invented.
Qualified a2a reuse without requested Host data remains supported. A stopped
target has no current execution to compare with a historical Host row.

This reader's coverage remains partial. Independently qualified positive absence
can permit a specific start without claiming fleet completeness. A source's
legitimate wider values are preserved; a narrower caller/selector identity that
cannot be represented is unavailable, with no truncation or new default.

## Resolve and revalidation

`Resolve` rechecks the typed Prepare against the same admitted local caller and
project, normalizes the complete request and computes the original digest. It
reads owner data once, reads each explicitly admitted Host feed once, then
evaluates the current clock. It brackets the complete acquisition with clock
readings: a zero clock is unavailable, and a backwards clock refuses with
`source_stale` before selection or retained-target revalidation can succeed.
The same rule covers optional Host reads. It neither retries a source nor
changes its observation/expiry or adds a lifetime. Equal readings remain valid;
forward movement evaluates the actual source expiry. The rollback comparison
uses wall time even when the clock supplies monotonic readings. Hard gates
precede the unchanged selector's reuse-before-start preference and deterministic
opaque-ID tie-break.

The resulting proposal retains the selector basis, complete decision and source
snapshot under `router-workbench-source-snapshot/0.1.0`. Its validity is no later
than either the required check expiry or the owner state/mapping witness expiry.
The operation engine reserves and freezes these values before any lifecycle
preparation. The bounded snapshot reference covers the whole normalized content;
full references are not discarded to fit its reference list. Aggregate durable
record capacity remains the operation engine's separate admission check.

`Revalidate` restores that exact retained proposal and requests current evidence
with the original selection as a narrowing selector. It does not invoke
`selection.Select`. It requires the same full mapping, workbench and known
execution, current request/card/task/policy and all action-specific gates. A
newly eligible alternative cannot replace the original target. Missing, changed
or stale evidence refuses the next action; it never changes the request or
proposal. Status does not call this planner: it observes only the original
operation through the separate lifecycle port.

## Validation

Run `go test ./workbench -run '^TestSourcePlanner' -count=1`, `go test ./...`,
`go vet ./...` and `go test -race ./...`. Tests pair qualified reuse/start with
every relevant missing/denied/unknown/conflicting/misbound/stale check, true state
expiry, full artifact identity, permutations, generation/mapping changes, partial
DATA provenance, stop facts, both Host execution-binding fields, unsupported
cross-source identity links, invalid source bindings and inert replay/Status. Engine docking
uses real parser, record and selector code with fixture owner/lifecycle ports;
it does not qualify production receipt acquisition or a live member.
