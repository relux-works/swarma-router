# Go component usage

Use these exported packages from module `relux.works/swarma-router` when working
in the repository or embedding its libraries. They implement bounded local
components, not the complete proposed Route/Recommend/Explain/Apply/Status
transport. They add no operations to `queued-task-handoff/0.1.0`. Source presence
and passing examples do not establish installation, production source admission
or a live-ready workbench.

The locators below are **repository-relative**, from the swarma-router repository
root. They are not bundled skill references: a separately copied skill needs a
matching repository checkout to read the docs, inspect the exported source and
run examples. Current component docs and code govern these entry points; the
bundled proposed full-interface design is not their Go API contract.

| Need | Entry points | Repository docs and source |
| --- | --- | --- |
| Consume owned-execution DATA | `inventory.Read`, `inventory.Source.Feed` | `docs/owned-inventory.md`, `inventory/inventory.go` |
| Propose reuse/start from normalized evidence | `selection.Select` | `docs/selection.md`, `selection/selection.go` |
| Reduced admitted launch and observation | `launch.Router.Prepare/Start/Observe`, `launch.Curator` | `docs/router-launch.md`, `launch/types.go`, `launch/router.go`, `launch/curator.go` |
| Retain and reconcile one workbench operation | `workbench.Parse`, `workbench.Engine.Run` | `docs/workbench-wire.md`, `docs/workbench-operation.md`, `workbench/engine.go`, `workbench/operation.go` |
| Recommend an inert local delivery draft | `localworkbench.LocalPlanner.Recommend/Revalidate` | `docs/local-workbench.md`, `localworkbench/types.go`, `localworkbench/planner.go` |

## Inventory: validated DATA, including unknowns

Call `inventory.Read(ctx, source, expectedBinding)` with an explicit finite
deadline and an admitted owner-side `Source`. Supply `Binding` out of band:
allowed source versions, owner receipt, member/project/workbench and exact
execution/runner. A frame cannot choose these bindings or a source executable.
The trust mode remains `local_single_user_unverified`; equality and valid JSON
do not authenticate the caller or producer.

`Feed(context.Context, io.Writer) error` must honor cancellation, propagate
write errors, complete synchronously and return nil only after successful full
production. It must not retain the writer. A complete frame followed by a source
error yields `unavailable/source-unavailable` with no projection. A callback
ignoring its context can block: Read provides no process watchdog or cleanup.
No production Feed adapter is shipped.

The bounded contract is `router-host-inventory/0.1.0-draft.1`: one strict JSON
object plus LF, at most `inventory.MaxFrame` (65536 bytes), exact envelope and
twenty-one fact keys. `usable` carries a validated projection;
`unavailable`/`conflict` carry no projection. Consult the inventory doc for the
closed reason set and admitted fact/provenance forms rather than inventing fields.

`owned_execution_partial` never establishes complete absence. Configured model
and requested effort remain configuration; thread-start-confirmed native values
remain last-confirmed. Effective effort, messaging binding and readiness are
unknown-only. Nullable source observation/expiry stay unknown; `CollectedAt`
does not refresh them. An expired fact can be valid DATA while being unusable
for an action. Neither idle activity nor stop intent proves stopped or ready.

## Selection: a proposal without effect authority

Call `selection.Select(selection.Input{...})` with an explicit `Now`, source
`Scope`, full `Basis` and exact candidate identities/configurations. The admitted
normalizer must verify actual sources and preserve unknowns. Opaque digests and
references bind data; they are not authentication or cryptographic verification.

For both actions, the required checks are caller authority, task constraints,
card member, profile/runtime, quality, provider limits, manager custody, board
assignment and messaging admission. Reuse additionally requires current
configuration, exact generation, ready response and occupancy. Start additionally
requires complete owned absence, preparation authority and launch capability.
Use the exact gate names and evidence types in `docs/selection.md` and the example.
Every required allow witness binds the full basis/candidate, has a real nonzero
observation no later than `Now`, and expires after `Now`. Missing, unknown,
conflicting, stale or mismatched evidence cannot become an allow.

Eligible ready/reuse precedes stopped/start; equal actions sort by opaque ID.
An unknown alternative does not block another eligible candidate. Results are
`reuse`/`start` with copied identity, required evidence and earliest expiry;
otherwise `insufficient_evidence`, `defer` or `refuse`, with no selected identity
or usable validity. Invalid input refuses with `invalid_input`. This is neither
cost optimization nor a reservation, readiness receipt or launch permission.
Before effects, the execution owner independently revalidates actual authority,
current sources, task state, custody, holds and the exact tuple.

## Launch: explicit ports and retained uncertainty

Configure `launch.Router` with fixed manager/project, `Plugin`, `Guard`,
`OpenStore` and `Now`. The reduced contract is `router-launch/0.1.0`.
`Prepare(intent)` is pure plugin translation, without Guard or store calls.
`Start(ctx, intent)` serializes the original key, checks current exact admission,
persists uncertainty before one plugin effect attempt and retains its result.
`Observe(ctx, intent)` reconciles the original operation; it never calls Start.
Matching Start replay returns `Historical=true`, not current liveness. Changed
intent conflicts; unreadable or uncertain state prevents another attempt.

The shipped `launch.Curator` implements `Plugin`; `CuratorConfig` binds trusted
executable/home/path/permissions/network and optional target identity selectors.
`Backend` supplies session/process observations and creation (`SystemBackend`
is the concrete tmux backend). `Ready.Read` must independently authenticate
fresh exact runtime/binding/lane evidence, or native assignment/handoff evidence.
No production binding-reader adapter is shipped. The Guard owns current
task/configuration/custody and positive stopped proof; unknown absence cannot
allow Start. `OpenFileStore` supplies private launch storage, separate from
queued-notice journals. Its local lock is not distributed fencing.

`attempted` is an attempt receipt; `pending` lacks complete independent readiness;
only a current qualified observation can report `running`. `unknown` preserves
possible effects or unresolved durability. Preserve the original configuration,
key and journal on uncertainty; no new correlation, journal reset or alternate
launch compensates for it. No result proves task acceptance or completion.
There is no stop/watchdog path or automatic retry. The plugin configuration hash
does not attest executable/profile bytes. Real runtime/source qualification and
safe cleanup remain the embedding owner's responsibility.

## Workbench: one retained operation

Parse a bounded JSON+LF frame with `workbench.Parse(frame, admittedLocalContext)`;
pass the resulting immutable `Request` and a finite-deadline context to
`Engine.Run`. Configure `Repository`, `Access`, `Planner`, `Lifecycle` and `Clock`
out of band. Inspect both the returned Go error and `Output.Bytes()` /
`Output.ExitCode()`; a nil Go error alone is not readiness.

For the `swarma-router/0.2.0-draft.2` operation core, the repository serializes
the exact caller/project/correlation tuple. It must distinguish reliable absence
from read failure and acknowledge commits only after atomic expected-revision
comparison and durable exact bytes. The engine retains the complete request,
first source snapshot, proposal, selection and exact lower mapping. It reserves
before source resolution and acknowledges a possible-effect marker before one
Start. Reuse observes without lifecycle Prepare or Start.

Matching Prepare is historical: storage read authorization still applies, but
there is no planner/lifecycle call or record write and `ready` is null. Changed
digest conflicts. Status observes only the retained operation; it cannot select,
prepare or start a replacement. Missing Status does not reserve. Lost write
acknowledgements and possible effects stay same-key uncertainty, without retry,
rollback, a new target or a new lower operation.

`SourcePlanner` joins admitted `OwnerReader` inputs, optional exact-bound
`inventory.Read` DATA and `selection.Select`. Its Revalidate narrows to the
retained target without reselection. `LowerLifecycle` docks `launch.Router`
using admitted mapping/authority/readiness from `LifecycleOwner`. Lower `running`
alone is insufficient: fresh upper readiness must bind the original request,
selection, configuration/lock, custody/task, runtime and messaging/reply evidence.
Native execution and messaging identities stay distinct. Production receipt
acquisition and complete live wiring are not supplied by these ports.

`FileRepository` supplies private local storage; `RetainedRequest` supplies only
the request-binding envelope, not durability by construction. Read the repository
locators `docs/workbench-file-repository.md`, `docs/workbench-retained-request.md`,
`docs/workbench-source-planner.md` and `docs/workbench-lower-lifecycle.md` before
embedding those adapters. Hash consistency cannot defeat a hostile same-user
writer. Synchronous ports must honor deadlines; the engine cannot contain an
uncooperative callback and starts no watchdog or retry loop itself.

Only durably acknowledged fresh complete readiness exits 0, rechecked after
commit and custody release. Historical/pending/unknown/Recovery exit 3;
defer/refuse/unavailable/conflict and pre-reservation errors exit 2. Preserve the
exact key/digest and confirmed references on Recovery. Ready does not assign,
post, establish recipient acceptance or complete a task.

## Local planner: inert recommendation and exact-target refresh

`localworkbench.LocalPlanner` is a separate API with `Local`, `Room`, `Reader`
(`LocalOwnerReader`) and `Clock` supplied out of band. Call `Recommend(ctx,
LocalRequest)` for a bounded source snapshot; call `Revalidate(ctx, request,
LocalSelection)` to refresh only the retained complete target/generation,
without reselection. Supply a finite-deadline context and a trusted UTC clock
with millisecond precision. Use `LocalRecommendation.Bytes()` for canonical JSON.
This is `router-local-owner-inputs/1`, not the effectful engine's wire contract.

Preserve observed, caller-asserted, unknown and deny evidence separately. Only
the documented gates accept caller statements; they are not authenticated
observations. The reader owns actual source checks/currentness and coverage.
Dynamic gates need actual source observation and admitted currentness; null
expiry stays null, with no invented TTL. A failed read or partial view cannot
establish complete absence. Concrete complete source readers are not supplied.

Only eligible results contain `selected` and `prepared_workbench`, including a
delivery draft. Every result has `execution_authority:false`. No command,
launch, post, reservation, storage or journal effect occurs. The caller separately
checks existing recipient membership, actual delivery binding and the exact
recipient's addressed acceptance; a draft or server receipt establishes none of
those outcomes. See `docs/local-workbench.md` for the sixteen gate unions and
source restrictions; do not transplant the selector's normalized Check structs.

## Executable pure examples

Repository locators `inventory/example_test.go` (`ExampleRead`) and
`selection/example_test.go` (`ExampleSelect`) are external-package examples using
synthetic in-memory inputs. The first pairs usable partial DATA with the same
valid frame followed by producer failure. The second pairs all fresh reuse
checks with just the ready-response witness made unknown. Their expected outputs
follow the documented completion and eligibility contracts; neither exercises a
production adapter or live action. Run from the repository root with its admitted
Go toolchain and dependency cache (the module declares Go 1.23.0):

```sh
go test -count=1 -run '^Example(Read|Select)$' ./inventory ./selection
go test -count=1 ./inventory ./selection
go test -race -count=1 ./inventory ./selection
go vet ./inventory ./selection
```
