# Deterministic selection component

`selection.Select` is a pure Go proposal provider for the proposed
[workbench interface](router-workbench-v2.md). It is a component candidate,
without a production source adapter, CLI, live qualification or execution path.
It proposes reuse or start and never starts, stops, changes, assigns or sends.

## Trusted input boundary

The embedding owner supplies `selection.Input` in process. Its normalizer must
authenticate caller/project, verify the exact task/cards/policy/qualification
revisions, admit each source and preserve its actual facts and unknowns. Decoding
arbitrary JSON into these structs does not authenticate it. Opaque revision and
digest references are bindings, not proof of cryptographic verification.

Input includes an explicit evaluation time and candidate coverage scope. Scope
is `partial` or `complete` exactly as supplied by the admitted source; neither an
empty candidate list nor merged partial observations proves fleet completeness.
References are nonempty valid UTF-8, at most 512 bytes, without control characters.
They are data and are never interpreted as commands, selectors or paths.

Each candidate names an opaque ID, member/workbench/card revision, and exact
runtime/vendor/model/effort/profile/lock configuration. This configuration is
the proposed target, not a claim that it currently runs. Each normalized check
has a closed name and verdict (`allow`, `deny`, `unknown`, `conflict`). An allow
witness must bind the full input basis and exact candidate identity, carry an
admitted evidence reference and actual source observation time, and expire after
evaluation. Future observations or unknown times cannot establish eligibility.
The selector cannot verify source trust or detect a normalizer inventing facts.
Source observation/expiry must not be replaced by collection time.

## Required evidence

| Action | Required normalized checks |
| --- | --- |
| Both | `caller_authority`, `task_constraints`, `card_member`, `profile_runtime`, `quality`, `provider_limits`, `manager_custody`, `board_assignment`, `messaging_admission` |
| Reuse | `current_configuration`, `exact_generation`, `ready_response`, `occupancy` |
| Start | `complete_owned_absence`, `preparation_authority`, `launch_capability` |

These are typed decisions from existing admitted sources, not a new registry,
quota service, reservation, permission token or discovery protocol. They retain
the accepted meanings: the card declares an admitted seat; the native board
holds task constraints/assignee/status; existing management owns custody; the
retained launch operation is the launch claim. Quality and provider restrictions
must be established before preference, without assuming a cheap model fits.

`complete_owned_absence` means current positive owning-source evidence that the
member session and binding are absent. A missing endpoint, stale row or partial
projection cannot allow it. Reuse requires current exact configuration and
generation plus an actual ready response and permitted occupancy; a configured
model, historical thread-start echo, PID or idle state cannot allow these checks.
Actual effective effort/profile/binding stay unknown without their admitted
witnesses. Existing admitted target messaging is required for caller delivery.

An optional check for another action does not gate the current action. For
example, unknown current observed configuration need not prevent an otherwise
qualified start on a positively stopped member, and stopped absence is not a
reuse requirement. Unknown facts cannot be passed as allowed checks.

## Selection and results

1. `ready` candidates are considered for reuse; `stopped` for start.
2. Relevant hard denials exclude a candidate. Missing, mismatched, unknown,
   conflicting or expired required evidence prevents its selection.
3. Eligible reuse precedes eligible start. Equal-action choices sort by opaque
   candidate ID in ascending byte order. This explicit tie-break makes no claim
   about relative price, quality or project-wide optimality.

An unknown alternative does not block another fully eligible option. Multiple
proposed configurations for one stopped member are legitimate. Contradictory
state, workbench or card for one member, or differing live configurations,
invalidate the input. Duplicate candidate IDs/checks and unknown enum/check
names also fail closed. Both actions return copied selected identity, exact
input basis/scope, required evidence references and their earliest expiry.

With no eligible option, a ready/stopped/unknown/conflicting candidate lacking
evidence yields `insufficient_evidence`. Otherwise running/starting candidates
yield `defer` with `change_unavailable`. No eligible candidate yields `refuse`;
empty coverage does not prove absence. Invalid input returns a closed
`invalid_input` reason. Unselected results carry no selected identity, evidence
or usable validity. Reasons contain no source diagnostics or supplied payload.

A proposal is not a ready workbench, permission, reservation or launch receipt.
The embedding operation must retain its exact request/source/decision bindings
and preserve replay semantics. Before any effect, the execution owner rechecks
current authority, source revisions, task state, custody, holds and exact tuple.
The caller subsequently handles its own board assignment, task message and
explicit recipient acceptance. None of those operations is implemented here.

## Validation

Run from the repository root using its Go toolchain:

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./selection -count=1
GOTOOLCHAIN=local GOPROXY=off go test ./... -count=1
GOTOOLCHAIN=local GOPROXY=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off go test -race ./... -count=1
```

Tests exercise independently enumerated required checks, fresh valid proposals,
hard denials versus unknown/conflict, every exact binding dimension, time and
reference boundaries, action-specific optional facts, input permutations,
contradictory member rows, no change transition and copied output data. These
isolated tests do not qualify production normalizers, live reuse/start or source
readiness. Linux cross-compilation, when reported, is distinct from execution.
