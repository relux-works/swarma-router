# Optional plugins and static configuration

## Declaration and registration design

Use [interface-contract.md](interface-contract.md), **Proposed adapter ports and capability negotiation**, **Closed router-facing port records**, and **Observation plugins and frozen feature inputs**. The accepted design defines no released plugin package ID, discovery command, config filename or installation route.

Each adapter declaration has exactly `{id, implementation_revision, interface, versions, capabilities, counterpart_contract, qualification}`. Ref and RevisionRef semantics apply; counterpart_contract/qualification permit explicit null. Capabilities use `swarma-router/0.1.0/<action>`. Interface/action versions and independent source-owner qualification bind the exact implementation/counterpart tuple; an adapter's self-reported flag, reference, version or digest is insufficient. Null/unverified qualification permits fixtures/shadow data only, never live admission.

The proposed configuration is `{contract,project,plugins:[{interface,adapter,version,configuration_ref}],policy}`. Select exactly one adapter for each required interface; reject duplicate interface selections, unknown fields and unsupported versions. Omitted optional interface is unconfigured, not empty successful data. configuration_ref is opaque module-owned data, not a shell/load path.

Initial registration means statically linking an implementation and exposing its declaration through the module's explicit interface selection. The registration function, language/package layout, resolver and validation entry point are **pending implementation**; do not invent register_plugin(), install commands, dynamic library paths or auto-discovery. After those exist, bind the configured adapter ID to exactly one declaration, verify selected version/action support and independent qualification, then validate inputs/results. Such future implementation needs its own review. This draft is not a working registration procedure.

## Responsibility and capability matrix

| Interface | Proposed actions / payload family | Admission boundary |
| --- | --- | --- |
| task-source/0.1, member-source/0.1, worker-source/0.1, catalog/0.1, usage-source/0.1, schedule-source/0.1, preferences/0.1 | source.read with ReadRequest and interface-specific SourceResult | Exact project/subjects/revisions/freshness; authoritative absence differs from unknown/unconfigured. No implicit board write, identity provision or scheduling. |
| policy/0.1 | policy.read_projection with PolicyProjectionRequest, then policy.evaluate with AdmissionRequest | SourceResult<Policy> acquisition is independent of SourceResult<Admission>. Declare both capabilities for live Route/Apply; exact pair and Snapshot.policy equality, then separately current owner effect authority. |
| vendor-observation/0.1, local-observation/0.1 | vendor.observe/local.observe with ObservationRequest | Exact request digest/type/scope/coverage/freshness; observations only. |
| estimator/0.1 | estimator.prepare | Preparatory frozen output bound to feature_input_digest/observation_digest/artifact. Not called by selection replay; calibration remains separately unqualified. |
| decision-store/0.1 | decision.put/decision.get | Complete bounded input+Decision, caller/project scope, exact digests, atomic storage/actionability. |
| operation-store/0.1 | operation.reserve/operation.cas/operation.get | Durable key/digest/owner correlation before ANY effect including claim; revision CAS and no reuse after lost dedup evidence. |
| claim/0.1 | claim.acquire/validate/retain/transfer/release/status | All exact owners/subjects/revisions/fences; no guessed lock/resource allocation. Preserve unknown custody. |
| launcher/0.1 | launch.preflight/start/stop/liveness and owner.operation_status | Exact card/configuration/operation/generation/claim/attenuation, positive liveness or absence; observational preflight is not launch. |
| lifecycle/0.1 | lifecycle.gate/quiesce/save/restore/reconcile_input/ready/resume and owner.operation_status | Closed, linked restore→input→ready→resume proof chain with successor gate and actual-time checks. |
| broker/0.1 | broker.deliver/broker.status | Exact task/generation/delivery/claim; queue acceptance differs from execution/ack/completion. No second terminal writer. |

Slash-grouped action names in this table are shorthand; actual strings are the individually spelled names in the canonical action table. No launch.relaunch action exists. A shared Launcher/Lifecycle implementation must declare and independently qualify both interfaces; it cannot borrow undeclared semantics from a counterpart.

Every port envelope is `{contract,interface,action,call_id,payload}`; reply `{contract,interface,action,call_id,result}` must echo exactly. Trusted ExecutionContext is out of band. Ordinary read/observation result types differ from effect results. Effect binding, OwnerStamp, claim and correlation checks in the contract are mandatory; a digest is not source authentication or effect proof. Lost echo/correlation means unknown, not proven no-effect refusal.

## Observation data must change decisions

Vendor observations join the ENTIRE QuotaKey: vendor, pseudonymous account_scope/pool_scope, dimension, unit, window_id/start/end. One fresh complete absolute observation per key; cumulative60 then70 is not130. Unknown/stale/partial/wrong-window is not zero/unlimited/exhausted. Current overload is independent from billing permission and future reset is no promise. Required task-specific upper needs must be independently supported; a missing requirement set cannot become explicit neutral policy.

Machine CPU/memory/VRAM/GPU/ANE/thermal facts and exact-runtime loaded-model/queue inventory have separate coverage/custodians. Missing row is absent only under fresh complete exact-runtime documented coverage. Loading/process name is not loaded; unknown GPU/ANE is not available zero. Do not subtract occupied bytes twice. Required residency/queue/resource checks operate directly on actual supported observations; observation grants no resource reservation/model load/eviction/launch.

Freeze actual normalized values, units, scope, freshness/completeness/unknown masks and requirement joins into feature preparation. Bind both feature_input_digest (Snapshot excluding estimator) and observation_digest (vendor_limits/local_resources). A matching digest does not prove learned use/calibration. Hard gates still apply when explicit labels bypass inference. [Conformance cases](conformance.md) vary observations while holding model labels fixed.

Reuse an independently admitted matching host feed when its supported semantics suffice; do not assume disabled consumption is a quota feed or create a duplicate live probe. Credentials remain in the adapter's custody. Do not project raw account names/tokens, private transcripts or secret command lines into records, examples or diagnostics.

## Source qualification remains pending

U01–U11 in the bundled contract retain card/member, task/assignment, liveness/slot, broker/correlation, executor tuple/attenuation, usage/quota, lifecycle, quality/cost/timing/authority, schedule, vendor and local/runtime inventory custody. Host identify/controller/retained-runner prototypes do not qualify liveness/allocation/general restoration; supplied Host usage is disabled. Curator resolve/planning/status is not installed readiness or a qualified exact executor. Current Pulse card validation/future identity design is not a scheduler. These are bounded pinned source observations, not permanent fleet-wide capability absence; obtain fresh source-owner admission before eventual live qualification.
