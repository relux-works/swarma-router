# Owned execution inventory DATA consumer

The Go package `relux.works/swarma-router/inventory` consumes the bounded
`router-host-inventory/0.1.0-draft.1` DATA projection. This is a consumer component
for the [proposed workbench interface](router-workbench-v2.md), not an installed
Host export, production source adapter, routing CLI or readiness provider.

The trust mode is explicitly `local_single_user_unverified`. The invoking owner
supplies its admitted identity/source configuration. A consistent input document
is not cryptographic caller authentication, ownership evidence or effect authority.
There is no process launch, source executable/file selector, private control socket,
task assignment, messaging, retry or source discovery in this package.

## Entry point and source completion

```go
result := inventory.Read(ctx, admittedOwnerSource, expectedBinding)
```

The context must have a finite explicit deadline. `expectedBinding` supplies the
admitted source version allowlist, existing owner receipt, stable member/project/
workbench, execution and runner references. None comes from request overrides.

`Source.Feed(ctx, writer)` is a trusted owner-side seam. It writes only the minimal
projection, honors cancellation/deadline and returns nil only after synchronous
normal completion. If a future admitted adapter captures a subprocess, successful
process completion must be established before Feed returns nil. A plain pipe's
EOF does not prove that process exited successfully. Feed must not retain the
writer, write asynchronously after return or disguise failed production as success.

The consumer allocates a fixed maximum-sized sink, refuses overflow and checks
completion/context before parsing or returning data. Even a complete valid frame
followed by a producer error returns no projection. A callback that ignores its
context can block its caller; this package does not launch a watchdog or kill a
process. A production source adapter must qualify its own bounded execution and
cleanup contract. No production source implementation is supplied here.

## Frame and binding rules

The frame is one valid UTF-8 JSON object followed by exactly one LF and complete
production, at most 65536 total bytes including LF, with nesting at most eight.
JSON whitespace inside the document is supported; trailing whitespace after its
closing brace, CRLF, a second LF/value, BOM, invalid encoding, truncated JSON,
duplicate decoded keys and unknown fields are refused. Paired UTF-16 escapes
remain valid Unicode; lone surrogate escapes are rejected instead of silently
replacing a reference. Strings reject C0/C1 controls.

Exactly twelve envelope fields are required:

| Field | Constraint |
| --- | --- |
| `contract` | Exactly `router-host-inventory/0.1.0-draft.1`. |
| `source_version` | Nonempty UTF-8 string, at most 256 bytes, matching admitted configuration. |
| `trust_mode` | Exactly `local_single_user_unverified`. |
| `owner_binding_ref`, `member`, `project`, `workbench` | Nonempty opaque strings, at most 512 bytes each, exactly matching the existing owner binding. |
| `execution`, `runner_instance` | Nonempty opaque strings, at most 512 bytes each, matching the exact execution binding. |
| `collected_at` | UTC RFC3339 timestamp ending `Z`, with optional one-to-nine fractional digits. Collection time only. |
| `coverage` | Exactly `owned_execution_partial`. No fleet completeness or stopped absence. |
| `facts` | Exactly the twenty-one entries below. Required keys may contain explicit unknowns. |

An unsupported contract or unadmitted source version is unavailable. Reference
strings are compared exactly and never interpreted as paths, commands, regexes
or identity selectors. Local equality does not authenticate arbitrary JSON.

## Fact forms and provenance

A known fact has exactly `known=true`, `value`, `provenance`,
`source_observed_at` and `source_expires_at`. Both timestamps are required keys
but independently nullable. Non-null values use the UTC grammar above. Observation
after collection or expiry at/before observation is conflicting evidence. A past
expiry by itself does not make the DATA frame malformed; an action must reject
that expired evidence separately. Parsing never refreshes any source timestamp.

An unknown fact has exactly `known=false` and `reason`. The closed reason set is
`not-exposed`, `not-observed`, `not-bound`, `unsupported`, `redacted`. It cannot
carry a value, provenance or timestamps. The three unknown-only facts below use
`not-exposed`. Known strings are nonempty and at most 512 UTF-8 bytes. Boolean
values are JSON booleans. Epoch is a canonical unsigned decimal **string** from
`0` through `18446744073709551615`, with no sign, leading zeros, exponent or fraction.

| Fact keys | Known value and admitted provenance |
| --- | --- |
| `controller_epoch` | Unsigned decimal string, `source_state`; never native or messaging generation. |
| `native_thread` | Observed nonempty string, `source_state`. |
| `selected_adapter` | String, `configured`; declaration, not executable attestation. |
| `observed_release` | String, `source_state`; stored adapter descriptor declaration, not actual binary/version observation. |
| `configured_vendor`, `configured_model` | Strings, `configured`, under the exact owned execution/driver invariant. |
| `native_vendor`, `native_model` | Strings, `thread_start_confirmed`, correlated to the exported native thread. Last-confirmed only. |
| `requested_effort` | String, `configured`; not effective observed effort. |
| `effective_effort` | Unknown only, `not-exposed`. |
| `profile`, `profile_lock`, `placement` | Strings, `owner_receipt`, from separately admitted existing evidence; otherwise unknown. |
| `native_activity`, `current_turn`, `input_state`, `fence` | Scalar strings, `source_state`; never business readiness, assignment, success or process absence. Empty current turn is unknown/not-observed. |
| `security_stopped`, `stop_requested` | Booleans, `source_state`; security/intent facts, not stopped-process proof. |
| `messaging_binding`, `readiness` | Unknown only, `not-exposed`; separate admitted sources are required. |

Known native confirmation needs an observed thread. Known configured/native
values that contradict the exact execution invariant conflict. Partial unknowns
do not discard unrelated usable facts. Source priority cannot hide a conflict.
The stored release descriptor may be `0.164.0`; that literal is not an executable
version check. Missing native times stay null, regardless of collection age.

## Results and validation

| Class | Closed reason / meaning |
| --- | --- |
| `usable` | Complete validated local DATA only; `Projection` is populated. Never readiness or permission to launch. |
| `unavailable` | `source-unavailable`, `deadline-exceeded`, `frame-invalid`, `frame-too-large`, `unsupported-contract`. |
| `conflict` | `owner-binding-mismatch`, `execution-mismatch`, `source-facts-conflict`. |

Every unavailable/conflict result has a nil projection. Reasons never contain
source payload, paths, commands, tokens or stderr. A consumer must preserve these
limits instead of turning a valid partial projection into an action claim.

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./inventory
GOTOOLCHAIN=local GOPROXY=off go vet ./inventory
GOTOOLCHAIN=local GOPROXY=off go test -race ./inventory
go doc relux.works/swarma-router/inventory
```

Independent contract fixtures are in `inventory/testdata/`. They are synthetic
examples of the agreed source forms, not actual-host observations. Tests pair
valid source forms with forbidden neighbors and assert exact closed refusal and
no partial projection. Producer completion, cancellation, bounds and concurrent
independent reads are exercised through isolated sources. They do not qualify a
production export, complete routing solution, source process or live workbench.
