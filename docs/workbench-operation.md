# Local workbench operation engine

The `workbench.Engine` Go library implements the same-operation portion of
`swarma-router/0.2.0-draft.2`. It consumes an immutable request from
[`Parse`](workbench-wire.md) and returns one bounded JSON+LF `Output.Bytes()`
frame with `Output.ExitCode()`. A finite caller context deadline is required.
The library starts no goroutine, watchdog, process, session or retry loop.

This is an injected-port component. A [private local file repository](workbench-file-repository.md)
implements its storage port. A source normalizer, selection/launch adapter and
CLI remain unimplemented. Engine unit tests use a serialized memory fixture;
repository docking tests use real private files and controlled source/lifecycle
ports. They do not establish machine power-loss behavior, source authenticity,
a live launch, or readiness of an actual member. The already released notice and
lower launch contracts, binaries and journals are separate and unchanged.

## Admitted ports

All ports come from trusted configuration outside request JSON. A request cannot
select a store, callback, executable, private control source or identity.

| Port | Required responsibility |
| --- | --- |
| `Repository.Open` | Serialize the exact caller/project/correlation tuple across cooperating writers until the returned journal closes. |
| `Journal.Read` | Return `nil,nil` only for reliable absence; preserve unreadable/ambiguous state as an error. |
| `Journal.Commit` | Compare the expected opaque revision atomically, call its builder exactly once with a distinct new valid revision, and acknowledge only after those exact bytes and revision are durable. An error may mean committed but unacknowledged. |
| `Journal.Close` | Release serialization. Failure returns same-key Recovery with confirmed references; it cannot retract an acknowledged commit or authorize another effect. The repository owner must recover failed release. |
| `Access` | Check current storage read, preparation and observation permissions separately. Pure historical replay checks storage access without source acquisition. |
| `Planner.Resolve` | Resolve exact admitted task/card/policy/inventory/qualification sources once, normalize a complete sanitized snapshot and select deterministically. Return `selected/ok` with `reuse` or `start`, or a terminal no-effect result. |
| `Planner.Revalidate` | Recheck current full revisions, holds, authority, custody, task state, configuration and action prerequisites without changing the selected proposal. |
| `Lifecycle.Prepare` | Construct the exact pure lower mapping. Resolve native names through qualified owner mappings; never treat request labels as argv or profile artifact digests as actual profile locks. |
| `Lifecycle.Start` | Use the retained lower operation and its existing Guard/journal. The upper engine calls it once only after an acknowledged possible-effect marker. |
| `Lifecycle.Observe` | Observe the original retained lower operation or reuse target. No selection, lifecycle Prepare, Start, stop, assignment, task/message post or replacement target. |
| `Clock` | Supply the actual evaluation clock for current observation validity. |

Ports are synchronous, must honor the context deadline and must not retain mutable
arguments. `View` exposes copied bindings. Hashes bind complete normalized bytes;
they add no authentication or source freshness. Raw private State, credentials,
transcripts or session payloads must not enter `Snapshot.Data`.

The existing lower Observe may internally call its pure retained-command helper
named Prepare and update its own original journal. Those source mechanics do not
authorize new preparation or effects. Upper observation writes use the original
operation and an expected revision too. An admitted owner readiness witness must
bind the full upper request/selected/lower operation, current configuration and
profile lock, custody/task state, actual runtime, messaging route and lane reply.
Lower `running`, a PID or a configured tuple alone cannot supply that witness.

## Ordering and retained identity

For a genuinely absent Prepare, the engine durably reserves the entire canonical
request before resolving a source. It then freezes the first snapshot, proposal
and selected configuration before pure mapping. Start freezes the exact lower
intent, mapping receipt and existing lower digest separately from the upper
canonical digest. It rechecks authority and current prerequisites, durably marks
possible effects, and calls Start once. Reuse observes without Prepare or Start.

The internal closed record contract is `swarma-router-operation/0.2.0-draft.2`.
It contains the original [retained request envelope](workbench-retained-request.md),
phase, first source snapshot/proposal/selection, lower mapping, action, monotonic
possible-effect and terminal-no-effect flags, first known execution identity,
last observation and receipt. Missing phase data is explicit null. Snapshots bind
their exact sanitized bytes with SHA256 and complete source references. A revision
is an opaque storage token with exact equality, never a sequence inferred by the
engine. The operation reference hashes the canonical complete lookup tuple.

The complete record is checked before every write and on every read: closed
fields/types, duplicate decoded keys, UTF-8/scalar escapes, embedded canonical
request and digest, snapshot digest, requested selection, lower intent digest,
phase invariants, observation/receipt agreement and known execution identity.
The repository still owns storage custody and authenticity. A valid hash does
not protect against a hostile same-user writer replacing a whole coherent record.

Matching Prepare returns a historical receipt with `ready:null`, without calling
any planner or lifecycle port or writing a record. A changed digest conflicts
without mutation. A reliable missing Status returns `operation_not_found` and
does not reserve. A crash before retained selection/mapping remains unknown;
Status cannot discover, select or prepare a replacement. Terminal pre-effect
results remain terminal. Status with a retained binding may obtain a fresh
same-operation witness and atomically update the original observation. It never
calls the planner, lifecycle Prepare or Start.

Initially unknown execution metadata becomes known only through a qualified
same-operation ready witness and is persisted before a fresh ready response.
Later witnesses must match all known identity fields. Host execution/runner/native
thread/controller epoch remain distinct from messaging session/thread/generation.
Missing or malformed evidence becomes unknown; stale or future-dated observation
validity cannot produce fresh ready. An adapter must conservatively floor finer
expiry precision and ensure every relied-on source remains valid.

## Failure and output bounds

A failed or lost reservation/write acknowledgement is uncertainty under the same
key and digest. No automatic retry, new target, correlation, lower operation,
journal, stop/reset or rollback follows. Interrupted Start leaves its already
durable intent/possible-effect marker for read-only reconciliation. The engine
does not catch arbitrary port panics and pretend they were a successful result.

Records are bounded to 262144 bytes, normal responses to 131072 and Recovery to
4096, including LF. Before Start, the minimal uncertainty update is measured with
the largest permitted encoded opaque revision, rather than a guessed margin.
An oversized actual observation or response after effects returns same-key
Recovery and preserves retained lower facts. Recovery can omit optional lower,
revision and operation references in that order to fit; it never truncates or
drops the exact lookup tuple/digest. The parser already proved the minimal exact
echo with the longest reason fits before a store is opened.

Only a durably acknowledged, fresh, complete ready receipt exits zero. Historical
ready labels, pending, unknown and Recovery exit 3; defer/refuse/unavailable,
conflict and pre-reservation errors exit 2. Historical ready never includes a
ready binding. These results do not assign a task, post a message, prove acceptance
or report completed work. The caller owns its separate current checks and actions.

## Validation

Run `go test ./workbench`, `go test ./...`, `go vet ./...` and
`go test -race ./...`. Build/test instructions and tools are in the root README;
temporary logs and controls belong under `.temp/`.

Paired tests exercise start/reuse, reservation and marker ordering, inert replay,
conflict, missing/unreadable/corrupt records, every write boundary before/after
acknowledgement loss, caller interruption, immutable retained fields, current
authority, pending-to-ready observation, distinct Host identities, changed known
generation, stale/future evidence and record/response/recovery limits. Two
cooperating concurrent callers execute one Start under the fixture's serialization.
These tests rely on the admitted port contract; they do not prove an arbitrary
repository or source adapter implements it.
