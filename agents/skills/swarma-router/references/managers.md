# Manager requests and truthful outcomes

## Current capability boundary

All examples describe proposed typed operations, not executable commands. The normative records are in [interface-contract.md](interface-contract.md), sections **Domain operations**, **Authority, snapshots and time**, **Durable operation receipts and reconciliation**, and **Closed router-facing port records**. A deployed entry point, source/adapter admission, durable stores and real lifecycle qualification are pending. Preparing a request does not authorize executing it.

## Choose the operation

| Intent | Proposed operation | Required inputs and decisive boundary |
| --- | --- | --- |
| Deliver an authorized task to its admitted card member | Route | contract, operation, request_id, project, exact task/card RevisionRefs, member, expected_session or explicit null, scoped idempotency_key. The router resolves liveness; the manager need not inspect it. |
| Compare keep/assign/switch/defer options | Recommend | Complete SelectionInput `{project,decision_time,snapshot,current_member,target_member}` and explicit record=store or skip. Snapshot contains all eleven typed source results; estimator output is prepared/frozen separately. Selection performs no source, launcher, broker or claim calls; record=store is an explicit separate storage attempt. |
| Inspect the original advice and inputs | Explain | project and decision_id plus common envelope. Only an authorized stored record qualifies; unavailable is not permission to recompute. |
| Execute a permitted configuration change | Apply | Stored unexpired switch decision_id; exact expected_task, expected_card and expected_session; scoped idempotency_key and common envelope. Current owner authority is separate from recommendation. |
| Recover a reserved action's actual outcome | Status | project and durable operation_id plus common envelope. Read failure is not absence and timeout is not cancellation. |

Every domain envelope includes contract=`swarma-router/0.1.0`, operation and request_id. All listed fields are required; null is explicit only where declared. Common RevisionRef, SessionRef, Time, Digest and Reason definitions are authoritative in the contract. Do not put caller authority, credentials or arbitrary extra fields into JSON: trusted ExecutionContext is out of band. Reject duplicate keys, floats/exponents, unknown variants and over-budget objects; never truncate to make a request fit.

## Route without manager liveness inspection

The following is a **syntactically concrete proposed request example**, with fictitious opaque references. Repeated `a`/`b` digests are illustrative data, not verified source content, admission or a live-ready request. Its envelope is complete; referenced source records and trusted execution context are intentionally not fabricated.

```json
{
  "contract": "swarma-router/0.1.0",
  "operation": "route",
  "request_id": "example:request-1",
  "project": "example:project",
  "task": {"source":"example:tasks","object":"example:task","revision":"r1","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "member": "example:member",
  "card": {"source":"example:cards","object":"example:card","revision":"r1","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
  "expected_session": null,
  "idempotency_key": "example:route-1"
}
```

With independently admitted fresh facts, running means one Broker delivery to the exact generation and zero Launcher calls, even when Launcher is absent. Stopped means positive absence, supported exact-card launch once, verified successor generation/liveness, then delivery. Unknown, stale/held/unknown-hold, wrong project/task/card/generation or missing Broker cannot cause a speculative launch. Resolve required capabilities before effects; read spare capacity does not acquire the task/slot claim.

First Route needs no previous Recommend/history/cache. Its assembly reads scoped Policy through policy.read_projection under source-owner read authority; Snapshot.policy is that validated SourceResult unchanged. The complete request/reply pair goes into AdmissionRequest before separate policy.evaluate and owner claim/boundary checks. Expected policy revision, full request digest, caller chain/attenuation, project/task/action/origin/destination and freshness must bind. Read permission or a successful read grants no delivery/start/assignment right.

## Recommend, Explain and explicit Apply

Freeze ALL semantic selection inputs, including explicit current_member/target_member nulls, exact task/card/worker/catalog/policy/usage/vendor_limits/local_resources/schedule/preferences/estimator results. A target constraint cannot be silently ignored. Sufficient explicit labels may bypass inference, never hard observation or authority checks. Qualify each candidate before costs; filter holds/dependencies/role/concurrency/owner runtime restrictions and exposed reviewer context. Unknown A cannot hide a separately usable B. A semantic QualificationKey is unique; conflicting or identical duplicates invalidate Policy rather than selecting the favorable row.

Compare conservative bounds on one supported basis including context reconstruction once; changed model/vendor is not free. Keep requires still-permitted current work and preserves any hold/input gate. Desired windows are soft priorities; physical launch and execution/resume both obey hard allowed windows and supported joint deadlines. next_check names information only, with no timer/subscription/reservation.

record=skip/unavailable is informational even if Decision.valid_until is future; Explain/Apply require a stored record. Pure replay uses the exact stored SelectionInput and frozen estimator, never live source/inference calls. Canonicalizer/selector execution remains unimplemented here.

Only same-member/same-slot/owner/placement replacement with exact current running predecessor is supported by Apply. Cross-member/running-other/different or unknown destination returns capability_unavailable before claim/gate/stop/launch/input; do not repair it with hidden Route. keep/assign/defer/informational decisions return decision_not_actionable. Stale stored identity/source/time returns stale_decision rather than recomputing.

A supported owner executes the full sequence: fenced claim → predecessor gate/quiescence/permitted handover → stop/positive absence → actual-time revalidation → one successor behind its own verified closed gate → authorized restoration → held-input reconciliation → exact-generation ready → acknowledged resume. Restore/reconcile/readiness failure preserves successor and closed gate. Lost resume acknowledgement means unknown gate/state, not guaranteed closure or success. No raw terminal write, foreign process control or implicit native resume command is part of this interface.

## Read receipts and recover

| Observation | Report and next proposed step |
| --- | --- |
| Durable queued/acknowledged delivery to exact generation | Route may succeed with exact delivery state; task execution/completion is not established. |
| Broker rejection after successor launch | Retained successor/claim means partial, preserve correlation; no rollback/redelivery invented. |
| Unknown launch/stop/reply or unreadable store | Same operation/owner Status; preserve bindings/claims. Unknown stop forbids successor launch. |
| RecoveryReceipt | Noncommitted unknown result, last known durable revision only; preserve operation_id and reconcile_required. No fabricated ledger revision. |
| Same scoped key and canonical payload, changed request_id only | Return/reconcile existing operation; no second effect. |
| Same scoped key, changed semantic payload | idempotency_conflict, existing operation unchanged. A new key cannot duplicate uncertain old work. |
| ready with closed gate | Not Apply success. After lost resume ack, Status must establish actual gate/generation. |
| Host/Curator refusal or unsupported tuple | Preserve sanitized refusal/capability gap; no alternate flags, runtime/model/provider or weaker privilege fallback. |

No cancel operation is defined. Timeout ends waiting only. Claim custody persists through dependent/uncertain effects or is explicitly transferred by its real owner; local expiry is not forced-release authority. Preserve caller attenuation and current owner admission at every real boundary.
