# Workbench lower lifecycle adapter

`workbench.LowerLifecycle` implements the operation engine's `Lifecycle` port
using the existing `launch.Router`. The embedding supplies fixed `Local`,
`Router`, `Owner` and `Clock` values. Request JSON cannot select a store, plugin,
callback, source, executable, native configuration or credential.

This is a trusted in-process adapter. `LifecycleOwner` must acquire and verify
actual allowed mapping, authority and readiness receipts. This component does
not implement production receipt acquisition, a Host command adapter, complete
repository/lifecycle wiring or a CLI. Its fixture docking never starts an actual
member, posts a task/message or qualifies a live workbench.

## Pure preparation and exact lower identity

Preparation restores the retained source-planner snapshot, complete selected
artifacts and proposal. It requires an exact owner mapping for the original
key/request digest/target digest, with a full immutable mapping reference and
actual source observation/expiry. The source verifies the native task, manager,
workspace/session and plugin mapping; labels and receipt hashes do not authenticate
that source. Native member/project/model/profile/runtime/effort/lock must agree
with the retained target and the fixed lower Router scope.

The source returns an empty lower correlation field. The adapter derives
`wb-<SHA256>` from the complete upper key, canonical request digest, selected
artifacts, full target digest, complete lower template and mapping reference.
It never truncates a wider wire reference into the lower grammar. The existing
pure lower `Prepare` checks its actual intent grammar and plugin configuration.
No lower store or Guard is called during preparation.

The returned `LowerMapping` retains the complete exact lower `Intent`, original
lower `Intent.Digest()` algorithm, plugin, complete lower operation reference and
mapping reference. The lower digest remains distinct from the upper canonical
request digest. Later calls recompute the derived correlation/reference from
these retained values; a transplanted key cannot select a different operation.
No mapping source is called again after preparation.

## Start and observation

Start requires the engine's retained possible-effect marker and exact mapping.
It calls the original lower `Router.Start` once, without an implicit Observe,
retry or alternate key. The per-call Guard wrapper requires a current owner
permit bound to the complete upper request/selection/target, retained mapping
and exact lower intent digest. It verifies actual source validity and positive
stopped state. The owner checks all current holds, task/card/policy/configuration,
custody and complete absence before issuing that permit.

An independently configured lower Guard retains its veto. Both permits must
match, and their actual validity intervals are intersected. The wrapper does not
change shared Router configuration or extend either source's expiry. Existing
lower uncertainty-before-effect persistence, historical replay and original-key
reconciliation remain unchanged. Missing authority cannot produce another effect.

Status invokes only original lower `Observe`. Its internal pure retained-command
validation and writes to the same original lower journal are allowed. Status
never calls the mapping source, lifecycle Prepare, Guard or Start. Reuse invokes
no lower method or store: it checks the original ready target and exact original
execution union, keeping native Host identity and messaging identity distinct.

## Upper readiness remains separate

The owner witness source receives a copied View and exact complete lower Result,
or nil for reuse. It must verify actual current task/custody/configuration/lock,
runtime, messaging route/session/thread/generation and addressed lane reply,
and correlate that evidence to the lower result's exact process and operation.
`LowerResultDigest` hashes the complete fixed-struct JSON encoding of that Result,
including integer process IDs, command and process birth/executable identities.
This content binding is not authentication or a substitute for source checks.

The adapter checks the result binding, complete upper observation shape, original
selected/lower identity, previously known execution and actual source validity.
A start can return upper ready only with a current non-historical lower running
result and complete bound attempt plus the independently verified upper witness.
Lower running alone stays unknown. A start normally returns pending first;
Status can then establish current lower liveness and upper readiness. A stale,
misbound or changed witness refuses readiness. An absent/failed witness returns
unknown with ready=null and no invented observation/expiry. Reuse cannot change
any original execution field, including a Host messaging generation.

## Validation and limits

Run `go test ./workbench -run '^TestLowerLifecycle' -count=1`, `go test ./...`,
`go vet ./...` and `go test -race ./...`. Tests dock real parser, engine,
source planner, selector and lower Router with isolated owner/store/plugin ports.
They cover exact mappings and native grammar boundaries, both guards, witness
binding/validity/generation, no-effect reuse, inert replay, original-key Status
and recovery after lost lower/upper acknowledgements without another Start.
The isolated result-binding removal control retains valid readiness and detects
wrong-result acceptance through the intended assertion.

The local cooperative storage/source trust model remains explicit. These Go
interfaces do not authenticate an arbitrary implementation. Ports must complete
synchronously and honor finite caller deadlines; this adapter starts no watchdog,
background poller or cancellation goroutine. It does not implement or qualify
production acquisition, actual native ancestry, readiness, delivery or adoption.
