# Local input planner

`localworkbench` implements the internal `router-local-owner-inputs/1` snapshot
model. It recommends a target and prepares data for the caller to inspect. It
has no posting, launch, reservation, storage, journal or effect adapter. The
existing workbench prepare/status wire and effectful engine are separate APIs.

The local model preserves four distinct evidence classes: `observed`,
`caller-asserted`, `unknown` and `deny`. Observed means an admitted reader actually
read and checked its source; it does not mean cryptographically authenticated.
Unsigned card declarations remain unsigned. A caller statement is recorded
verbatim, with its exact full caller identity and receipt time, without a source
expiry or an authenticated-witness wrapper.

## Embedding

Supply `LocalPlanner.Local` (the exact caller MXID and project), `Room` (the exact
intended existing room), `Reader` and `Clock` out of band. Request bytes cannot
choose these dependencies, source commands, paths, executable images or identities.
Use a bounded context and a trusted UTC clock with millisecond precision. Call
`Recommend` with a `LocalRequest`, and use the result's `Bytes` method for canonical
JSON with UTC-millisecond times and an explicit false execution-authority field.

`LocalRequest` has exact caller/project/correlation/task identities, an explicit
card set (member key, full MXID and source byte SHA256), explicit complete
configuration alternatives, and the objective. It does not invent a task
revision from a four-field task lookup. `RequestDigest` canonicalizes semantic
sets; `TargetDigest` identifies the complete target binding. Neither hash grants
authority or verifies a producer.

The source port is:

```go
type LocalOwnerReader interface {
    Read(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error)
}
```

The reader must verify actual admitted sources and their currentness/coverage
contracts. It must finish synchronously and retain no mutable arguments. Its
`only` parameter narrows a refresh to the retained complete target. `Revalidate`
passes that selection, rejects a different target or generation, and performs
no reselection or effect. Reader arguments/results are copied at the boundary.
Cancellation is checked before and after acquisition and before returning a
result. The port cannot contain an uncooperative or malicious callback; admission
of that callback remains the embedding's responsibility.

The package supplies the typed port, validator, inert planner and internal
`DecodeInputs` loader. It does not supply real source readers for quota, custody,
runtime, qualification or readiness. Tests use synthetic readers. Existing
partial task/self-card adapters supply only their documented facts; joining
their outputs does not complete these eligibility predicates.

## Gate representation

Every target has exactly sixteen distinct gate records. Each record binds the
complete request and target digests. An evidence union has only its class's
fields:

| Class | Exact evidence fields |
| --- | --- |
| observed | `kind`, `source`, `projection` |
| caller-asserted | `kind`, `asserted_by`, `statement`, `recorded_at` |
| unknown | `kind`, `reason` |
| deny | `kind`, `reason`, `basis` |

A denial's basis is one observed projection or one permitted caller statement,
never another denial/unknown chain. Unknown is distinct from observed false.
A false observed predicate must be reported as denial, rather than a positive
observation. Malformed/conflicting records return `source_conflict`. Relevant
unknown records yield `insufficient_evidence`; a relevant denial excludes the
action. Irrelevant gates must be exactly unknown/not_required. Any other class
on an irrelevant gate rejects, including an assertion permitted for the other
action.

| Gate | Action | Assertion allowed? | Observed projection beyond `subject` |
| --- | --- | --- | --- |
| caller_authority | Both | Yes | configuration, task_id, statement |
| task_constraints | Both | Yes | configuration, task_id, statement |
| card_member | Both | No | declaration (role, nullable project) |
| profile_runtime | Both | No | exact configuration |
| quality | Both | No | configuration, qualification record/hash/verdict |
| provider_limits | Both | No | configuration, complete scoped quota and unit |
| manager_custody | Both | Yes | configuration, task_id, statement |
| board_assignment | Both | Yes | configuration, task_id, statement |
| messaging_admission | Both | No | exact existing room/MXID, joined and complete |
| current_configuration | Reuse | No | declared configuration plus sourced live configuration/execution |
| exact_generation | Reuse | No | native identity, thread, birth and binding generation |
| ready_response | Reuse | No | execution and exact sender/recipient/task/request response |
| occupancy | Reuse | No | execution and observed availability |
| complete_owned_absence | Start | No | exact seat/MXID, scope, complete absence |
| preparation_authority | Start | Yes | configuration, task_id, statement |
| launch_capability | Start | No | qualified route and exact configuration |

`subject` always contains exact member, full MXID and source-card digest. The
projection is itself a closed union: unrelated fields reject. Full identities
are never reduced to localparts, normalized, trimmed or inferred from a label.
A matching localpart on another server is a different member. Configuration and
execution identities are separate, and every applicable projection must match
the complete requested binding.

Reuse has nine common plus four reuse gates, including four permitted common
statements. Its start-only preparation_authority is unknown/not_required.
Start has nine common plus three start gates, including those four statements
and preparation_authority. It additionally needs observed quota, qualified
capability and positively complete same-seat absence. A missing binding, failed
read, partial fleet view or absent Host row is not that absence. A live identity
cannot take a start fallback. Distinct stopped configuration alternatives for
one absent seat remain legitimate alternatives.

Sources are positively restricted by gate to the named Pulse self/discovery/
environment, task-board, Curator catalog, Host or a2a contracts. The full source
record has kind, admitted nonsecret locator, actual source/projection SHA256,
read time, nullable source observation time and nullable source expiry. A source
label/hash supplied by an untrusted caller does not establish an observation.
Static current reads can preserve null source times. Dynamic gates require an
actual source observation time and the reader's admitted currentness contract.
An applicable source expiry is checked; no default TTL or collection-derived
expiry is created. A null expiry stays null. Source time does not promise future
validity, and the embedding must refresh its actual checks before an effect.

Profile labels alone do not prove qualification. A card identity alone does not
prove joined room membership or a live runtime. `ready_response` must represent
the exact target's fresh addressed response to the readiness request. Its
positive response flag is not inferred from delivery, an inbox receipt or a
running process, and it is not task completion.

## Recommendation and delivery

Eligible reuse wins over eligible start. Equal-action candidates sort by their
complete canonical binding; this is deterministic selection, not cost scoring
or global optimality. Missing relevant evidence yields insufficient_evidence;
observed busy live work yields defer when no missing evidence or eligible target
remains. Fully observed exclusion yields refuse. An empty source result remains
insufficient evidence even when the reader's scope says complete. Partial scope
is preserved; no fleet absence is inferred.

Only eligible output has `selected` and `prepared_workbench`. The latter contains
caller/project/task/correlation, selected member/full MXID/configuration, a
recommendation digest, and a delivery draft (existing room, full recipient,
request/target-bound task text). It contains no executable command, launch key,
receipt witness, ready flag, durable revision or callback. Other outcomes return
no selected workbench or positive draft. Every result has
`execution_authority:false`.

The caller owns actual sending and effects. Before posting, verify the exact
existing recipient membership. Afterwards verify actual event body/sender/full
recipient/reply binding. Only the exact target's subsequent addressed acceptance
of the same task/request/configuration counts as acceptance; server delivery
alone remains delivery. Launches separately follow the caller's admitted
Curator/custody/quota/binding rules. Recommendation, actual launch, ready reply,
accepted assignment and work started are separate facts.

## Bounds and validation

The request is at most 64KiB canonical JSON, with at most sixteen cards and
configuration alternatives. The complete snapshot and output are at most 1MiB,
with at most sixteen targets and sixteen gate records per target. Assertions and
objective are at most 4096 UTF-8 bytes each; opaque references are at most 256.
Unsafe display controls/scalars and ambiguous identities reject. Valid opaque
punctuation is preserved. Snapshot decoding rejects duplicate decoded keys,
unknown/omitted fields, invalid scalar escapes, noncanonical times and numeric
overflow without a partial positive result.

Run `go test ./localworkbench`, `go test -race ./localworkbench` and
`go vet ./localworkbench`, or include the package with `go test ./...` and
`go vet ./...`. Tests pair valid reuse/start with every required gate omission,
forbidden/irrelevant assertions, generation/domain/configuration conflicts,
unknown and denied capacity/absence, retained-target refresh, cancellation,
strict decoding and byte bounds. A structural import test checks the bounded
pure dependency surface and detects a known os/exec control plant; it does not
claim to constrain a malicious admitted reader. Source guard-removal controls
and their logs are validation artifacts, not production behavior.
