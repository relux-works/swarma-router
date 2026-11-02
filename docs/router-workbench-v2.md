# Router workbench contract v2

Status: proposed and unimplemented, `swarma-router/0.2.0-draft.1`.
This successor top layer is a design, not an installed API, provider
qualification or permission to launch a session.

Owner direction, 2026-11-02:

> «в роутер приходит агент какой-то с задачей и карточками работников»

> «и он уже от своего имени выставляет ему через а2а задачу и проверяет что тот задачу принял»

The caller supplies a task and worker cards. Router obtains current workbench
inventory, asks a decision provider what fits, prepares or selects an admitted
workbench through a launcher, and returns the member and verified readiness to
the same caller. **The caller posts the task in its own identity and verifies
acceptance. Router sends no task message to the target.**

## Version boundary

This top layer supersedes the proposed request/delivery boundary of the
[0.1.0 design](../agents/skills/swarma-router/references/interface-contract.md)
only when this successor is agreed and qualified. The predecessor remains
immutable evidence; stored decisions, receipt digests and envelopes are not
converted. Its Route delivery behavior must not silently appear under v2.
This document reuses the predecessor's reference, revision, timestamp and digest
conventions for the named references below, not its operation schema.

`router-launch/0.1.0` is a separate lower layer: Prepare translates, Start records
an attempt, and Observe checks readiness separately. An attempt is insufficient
for a v2 ready result. Trusted admission/readiness adapters and the actual tuple
still need qualification. `queued-task-handoff/0.1.0` remains a caller-owned
notification helper; queued notification is neither readiness nor acceptance.
No historical real delivery is replayed to qualify v2.

## Operations and authenticated caller

The proposed methods are `prepare_workbench` and `status`.

| Prepare input | Required meaning |
| --- | --- |
| `contract` / `operation` | Exactly `swarma-router/0.2.0-draft.1` / `prepare_workbench`. |
| `correlation_id` | Durable operation identity scoped to authenticated caller/project. |
| `caller` / `project` | References matching the trusted transport execution context. |
| `task` | Existing native task revision/digest and authorized task content or readable reference. |
| `cards` | Exact worker-card revisions/digests for candidate members of that project. |
| `constraints` | Explicit admitted configuration limits and requested objective/policy revision. |

A field in arbitrary JSON cannot authenticate a caller. The embedding host
supplies trusted ExecutionContext binding caller, project, attenuated authority
chain, current policy and clock. Effective privileges cannot exceed that chain
and the admitted target profile. No request boolean, classifier recommendation
or card grants launch, switch, billing, identity or permission authority.

Source adapters verify task existence/state, dependencies and assignment
constraints, plus the supplied card revisions and member admission. Cards
identify stable members, roles/profiles and declared restrictions; they do not
prove live configuration, provision staff or create an account. Unknown or
conflicting task/card revisions refuse effects. A partial candidate set is
explicitly scoped and cannot claim project-wide optimality/completeness.

`prepare_workbench` may select a prepared workbench or request one admitted
preparation. It grants no implicit restart, model switch or staffing operation.
`status` carries the same caller/project/correlation and expected request digest;
it reconciles the retained operation and never starts another preparation.
Arbitrary argv/env, credentials and free-form permission overrides are absent.

## Inventory seam

`Inventory.Observe(project, members)` returns a source-attributed snapshot.
The session host is the preferred provider for sessions it actually manages.
An admitted `a2a who` adapter is a fallback observation source, not a complete
replacement for actual configuration/process evidence. Product card or Pulse
declarations describe intent, not observed model/profile/session state.

A snapshot names provider/version, revision/digest, project/member scope,
observed time, expiry, coverage boundaries and completeness. A workbench row has:

- stable member/workbench references, execution owner and placement;
- actual session/generation and source-supported state: ready, running,
  starting, stopped or unknown;
- observed runtime, model, effort and profile/lock, each explicitly known or
  unknown, separately from declared configuration;
- task occupancy/context revision and readiness/binding evidence references
  where the source is authorized and able to provide them.

A host covers only its own sessions unless wider coverage is demonstrated.
Missing, stale or failed lookups are not stopped. Positive absence requires
complete, fresh proof from the owning execution source. Fallback cannot invent
a model/profile from a declaration, or a session generation from a liveness row.
Disagreement between sources remains unresolved; provider priority never hides
a conflicting fact. Combining partial projections cannot invent completeness.

Fallback may preserve useful positive evidence during a primary outage. For
example, reported member liveness with model unknown is useful but insufficient
for configuration-specific readiness. Only facts needed by the selected action
must be established through existing admitted sources; an unused optional field
need not block it. Adapter commands/schemas remain subject to source agreement.

## Decision seam and context preference

`Decision.Select(task, cards, inventory, policy, qualifications)` is pure. It
returns a proposal bound to those exact revisions/digests, source expiries,
policy and evidence. The small classifier is one possible provider; this design
does not install a model or let its output mint permissions or quality evidence.

A proposal is `reuse`, `start`, `change`, `defer`, `insufficient_evidence` or
`refuse`, with a selected member/configuration where applicable, reasons,
validity and required capabilities. Configuration is the exact runtime/vendor/
model/effort/profile tuple. Fit includes role, task quality, context constraints,
profile/runtime compatibility, provider quota/billing and current owner policy.
An unknown requirement cannot be guessed away because the task looks easy.

Apply the owner's preference after hard eligibility:

1. Reuse an already suitable ready workbench when policy and occupancy permit.
2. If project worker quota has room and an admitted candidate member has no
   workbench up, prefer a fitting new workbench over changing a live worker's
   model/vendor and discarding its context.
3. If no eligible new-workbench option exists, propose change only under its
   separately admitted transition policy/capabilities. It is never an automatic
   retry after a refused or uncertain start.

Project worker-slot quota is distinct from provider usage quota, paid-plan limits
and host load. Its authority must define the counted unit/scope, allocations
and in-flight claims. Unknown quota is not spare capacity. Starting a dormant
admitted card member does not provision a new worker. Any fitting model/vendor
still satisfies authority, quality and configuration restrictions. This v2 rule
does not retrofit a host-load gate into the reduced launch library.

## Launcher / dispatcher seam

`Launcher.Prepare(proposal, execution_context, expected_state)` prepares the exact
selected member/configuration or returns an already prepared workbench. It
retains a same-operation reference and truthful phases. Observe is separate.
The launcher does not post or assign the task.

Before an effect, the execution owner revalidates all relevant fresh source
revisions, caller authority, holds, assignment constraints, profile/configuration,
canonical session, expected generation and exclusive member/slot custody. An
existing qualified manager custody projection can satisfy this requirement;
no new registry, daemon or distributed-lock implementation is mandated here.
The requesting manager owns session cleanup.

For reuse, verify exact generation/configuration/readiness and allowed occupancy
without restarting or injecting input. For start, positively establish member,
session and binding absence under existing exclusive preparation authority,
then invoke one exact launch. For change, require the owner's admitted gate,
quiescence, handover, stop/absence, successor, restoration and reconciliation
contract. A start-only Curator plugin does not implement that transition:
unsupported change is unavailable with zero stop/start effects. Do not simulate
it by starting over a live session or selecting another target after uncertainty.

The attenuated caller chain reaches execution. A privileged service identity
cannot broaden it. Target identity selectors belong only to the member's admitted
environment; Router never borrows them for messaging or readiness queries.

## Ready result and caller-owned assignment

A ready result contains contract, caller/project/correlation, request digest,
exact task/card revisions, selected member, actual workbench/session/generation,
configuration/profile lock, execution owner, readiness evidence and expiry. It
also names the owner's admitted exclusive assignment/custody window binding
this caller/task/member/generation. Two callers must not receive an unqualified
exclusive handoff to the same occupancy. Existing qualified manager custody can
provide the window; a local operation record alone is not proof of exclusivity.

Ready requires current positive runtime/configuration evidence, the member's
exact binding/lane response and its existing admitted a2a identity/project-room
delivery route. The lower launch layer's native assignment/handoff evidence can
describe a non-chat workbench, but cannot alone make it a caller-deliverable v2
target. Missing messaging admission is unavailable, not permission to provision
an identity or borrow one. A PID, tmux creation or exit zero is insufficient. Unknown
or stale evidence cannot mean ready. Historical replay is marked historical and
never gives fresh assignment authority. Other results are pending, deferred,
refused, unavailable, conflict or unknown, with bounded reasons and any known
same-operation reference. A decision proposal is not preparation readiness.

The caller receiving current ready evidence:

1. Rechecks the exact task/member/generation, custody window and expiry.
2. Records any required native assignment through its own existing authority.
3. Posts the task to the member in its own a2a identity.
4. Verifies actual sender/body/recipient/reply binding and the member's explicit
   acceptance through the established assignment route.

Router performs none of these board writes, task posts or acceptance assertions.
Ready is distinct from assignment, delivery, acceptance and completion. If the
caller's message outcome is uncertain, it retains/reconciles that original
messaging receipt; Router does not relaunch a workbench to retry task delivery.

## Durable identity and uncertainty

Key scope is authenticated caller/project/correlation. Request digest binds
version/operation, caller/project, task/card revisions and constraints. Retain
the first source snapshot, decision, qualification and execution references
separately. Refreshing sources cannot silently rewrite the original selection.
Changed request at the same key conflicts.

Persist intent/phase before effects. Same-request replay returns retained
historical facts with no new inventory selection, decision, preparation, switch
or message. Fresh status observes the same retained operation/generation only.
Lost replies, malformed receipts and failed post-effect persistence preserve
uncertainty and known facts. They cannot cause a new launch, target, correlation
or journal. Source drift before effects refuses stale admission; possible effects
require same-owner reconciliation. No rollback restores a forbidden predecessor
or bypasses a current hold. No stop, force-retry, reset or terminal-input escape
hatch is implied. Local durability is not distributed exactly-once execution
after journal loss or outside the admitted execution-owner custody boundary.

## Paired acceptance scenarios

These fixtures specify required outcomes; they do not claim executable/live
conformance.

| Valid control | Forbidden neighbor and expected outcome |
| --- | --- |
| Exact authenticated caller/task/cards | Forged caller or changed revision: refuse before effects. |
| Fresh exact host-owned ready tuple | Declaration/fallback lacking model or profile: no ready result. |
| Complete fresh target absence | Missing/stale/failed/partial inventory: never infer stopped. |
| Consistent source generation/configuration | Conflicting sources: resolve or report unknown. |
| Suitable ready member and allowed occupancy | Wrong tuple, held/busy member or unknown generation: no reuse. |
| Known free slot plus admitted dormant member | Exhausted/unknown quota or unprovisioned staff: no new launch. |
| Eligible new workbench preserves live context | Cheap unqualified tuple: exclude before preference. |
| Eligible new option versus disruptive change | Prefer new; do not discard live context merely to reuse a seat. |
| Fully admitted transition capability | Start-only plugin: unavailable with zero stop/start effects. |
| Exclusive caller/task/member/generation custody | Competing claimant or expired window: no exclusive ready handoff. |
| One start plus independent positive observation | Attempt/PID/exit zero only: pending or unknown, never ready. |
| Matching historical replay | Stale readiness: no fresh authority and zero new effects. |
| Same-operation status resolves known generation | Lost reply/changed generation: unknown; no second start/target. |
| Caller post then explicit member acceptance | Router post, borrowed identity or delivery=acceptance: forbidden. |
| Existing admitted target a2a delivery route | Native-only workbench without that route: unavailable, no identity provisioning. |
| Caller retains uncertain post receipt | Relaunching to retry a message: forbidden. |
| Immutable original request/configuration | Changed input at same key: conflict, no preparation. |

## Open source-agreement questions

These are proposed port requirements, not assertions about commands that exist:

- Inventory owner: exact supported public query and runtime/model/effort/profile-
  lock/generation provenance; coverage, freshness, readiness versus unknown;
  explicit limits for sessions it does not own and fallback composition.
- Project/assignment owner: authoritative worker-slot quota/counting and current
  exclusive task/member/generation custody, including in-flight work and expiry;
  keep it separate from provider quota and host load.
- Launcher owner: actual prepared-session observation/readiness facts and any
  admitted transition capability. A start-only route remains start-only.

Classifier implementation/model and actual live use require their own admitted
unit. Contract agreement precedes dependent adapter implementation; whole-solution
integration and independent exact-candidate review precede delivery. This design
grants no identity, staffing, private-source, trust, profile or service authority.
