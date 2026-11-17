# Read-only model recommendation

`router-recommend/1` is a file-input recommendation contract, separate from the
queued-notice CLI, launch libraries and proposed full routing transport. It
returns a decision only. It does not acquire live telemetry, launch or stop a
session, send a message, apply a switch, discover models, or change an assignment.
A recommendation always carries `execution_authority:false`.

## Invoke from a matching repository checkout

Prepare the public Go dependencies once with `go mod download`, then build offline:

```sh
mkdir -p .temp/bin
GOTOOLCHAIN=local GOPROXY=off go build -trimpath -o .temp/bin/swarma-router ./cmd/swarma-router
.temp/bin/swarma-router recommend --task task.json --member member.json --state state.json --policy policy.json --at 2026-11-17T11:00:00Z
.temp/bin/swarma-router recommend --task task.json --member member.json --state state.json --policy policy.json --at 2026-11-17T11:00:00Z --format text
```

All five input flags are required: `--task`, `--member`, `--state`, `--policy`
and `--at`. JSON is the default output; `--format json` selects it explicitly.
Use `--format text` for a compact human-readable decision; use JSON for
structured consumers. Text recommendations include status/action, selected
runtime/model/effort/reset, rule and reason; unsuccessful decisions include
status/code/missing/rule/reason and never a target. The timestamp is an
explicit RFC3339 evaluation instant; do not substitute the current clock during
replay. No policy, catalog, quota or model mapping is inferred from an installed
runtime, environment, model name or external service.

These commands require the recommendation engine and CLI in the same checkout.
Documentation and example data alone do not establish a callable binary,
installation, live telemetry, integration qualification or real staffing use.

## Input files and policy

Each input is an ordinary file of at most 1 MiB. A final symlink, FIFO,
directory or oversized file is refused. An absent file or required flag is
`insufficient_evidence`; invalid flags, format or time are refused. Diagnostics
do not echo input paths or file contents. Parent-directory custody remains the
caller's responsibility; these file checks are not source authentication.

Each file is a single closed JSON object with `contract:"router-recommend/1"`.
Use exact field names. Duplicate or unknown keys, case aliases, wrong
types, nulls, out-of-range values and trailing JSON are refused. Missing required
evidence is distinct from malformed or unknown configuration. Basis points are integers from 0 through 10000; 8000
means 80%, not 8000%. Provide real, owner-permitted policy and fresh telemetry
for a real decision. The synthetic files below illustrate the wire format only;
their model names, costs, ranks and quotas grant no admission.

| File | Required facts |
| --- | --- |
| `task.json` | `element_id`, `project`, `class`, `estimate_minutes`, `review_policy`. Work classes cover code, docs, research, review and metadata. |
| `member.json` | `role` and the exact `current` runtime/model/effort tuple. |
| `state.json` | `session_live`, `context_used_bps`, observation/expiry times, and scoped `provider_quotas` with usage, reset and observation/expiry times. |
| `policy.json` | Explicit catalog, per-role allowlist and ceiling, quota thresholds, context retention threshold and ordered preference rules. |

Every catalog row has an `id`, `runtime`, `vendor`, `model`, `effort`,
`capability_rank`, `relative_cost`, `quota_scope` and `billing`. The role's
`allowed_tuple_ids` and `ceiling_rank` constrain selection independently of
preference. Quota scopes join catalog rows, thresholds and telemetry exactly;
sharing a vendor does not imply a shared pool. Only included options are
eligible. There is no paid-credit fallback or quality-floor escape from a hard
gate. Ranks and relative costs are owner inputs, not estimates made by Router.

The first matching rule in file order wins. Rules match project, role, class,
review policy and the explicit estimate range;
`preferred_tuple_ids` is ordered. No matching rule produces
`insufficient_evidence`. All policy references must name known tuples, even in
unused rules. A preference never grants admission, changes a
ceiling or clears quota. Do not treat a fixture preference as an owner policy.

### task.json

```json
{
  "contract": "router-recommend/1",
  "element_id": "EXAMPLE-001",
  "project": "example-project",
  "class": "research",
  "estimate_minutes": 60,
  "review_policy": "none"
}
```

### member.json

```json
{
  "contract": "router-recommend/1",
  "role": "developer",
  "current": {
    "runtime": "codex",
    "model": "model-standard",
    "effort": "high"
  }
}
```

### state.json

```json
{
  "contract": "router-recommend/1",
  "session_live": false,
  "context_used_bps": 1000,
  "observed_at": "2026-11-17T10:55:00Z",
  "expires_at": "2026-11-17T11:05:00Z",
  "provider_quotas": [
    {
      "scope": "pool-a",
      "used_bps": 3000,
      "reset_at": "2026-11-18T00:00:00Z",
      "observed_at": "2026-11-17T10:55:00Z",
      "expires_at": "2026-11-17T11:05:00Z"
    },
    {
      "scope": "pool-b",
      "used_bps": 1000,
      "reset_at": "2026-11-18T00:00:00Z",
      "observed_at": "2026-11-17T10:55:00Z",
      "expires_at": "2026-11-17T11:05:00Z"
    }
  ]
}
```

### policy.json

```json
{
  "contract": "router-recommend/1",
  "catalog": [
    {
      "id": "small-low",
      "runtime": "codex",
      "vendor": "provider-b",
      "model": "model-small",
      "effort": "low",
      "capability_rank": 1,
      "relative_cost": 1,
      "quota_scope": "pool-b",
      "billing": "included"
    },
    {
      "id": "standard-high",
      "runtime": "codex",
      "vendor": "provider-a",
      "model": "model-standard",
      "effort": "high",
      "capability_rank": 2,
      "relative_cost": 2,
      "quota_scope": "pool-a",
      "billing": "included"
    },
    {
      "id": "large-high",
      "runtime": "codex",
      "vendor": "provider-a",
      "model": "model-large",
      "effort": "high",
      "capability_rank": 3,
      "relative_cost": 4,
      "quota_scope": "pool-a",
      "billing": "included"
    }
  ],
  "roles": [
    {
      "role": "developer",
      "ceiling_rank": 2,
      "allowed_tuple_ids": [
        "small-low",
        "standard-high"
      ]
    }
  ],
  "quota_thresholds": [
    {
      "scope": "pool-a",
      "used_bps": 8000
    },
    {
      "scope": "pool-b",
      "used_bps": 8000
    }
  ],
  "context_retention_bps": 9000,
  "rules": [
    {
      "id": "research-small-project",
      "project": "example-project",
      "role": "developer",
      "class": "research",
      "review_policy": "none",
      "estimate_min_minutes": 1,
      "estimate_max_minutes": 120,
      "preferred_tuple_ids": [
        "standard-high",
        "small-low"
      ]
    }
  ]
}
```

## Decision order and boundaries

Validate known configuration and required evidence first. Enforce the role
allowlist, ceiling and quota restrictions before context retention or
preferences. A known tuple above its role ceiling must move to a lower admitted
option; it cannot become permitted because of accumulated context. When both
ceiling and quota restrict the current tuple, the replacement must satisfy both.

Quota is restricted only when `used_bps > quota_threshold_bps`: 8000 at a
threshold of 8000 stays quota-clear; 8001 does not. A quota-driven replacement
must have strictly lower relative cost and clear included quota, or the result
is `defer`. It cannot use paid credits. Missing, future or stale relevant
observations produce `insufficient_evidence`, rather than zero usage or a
predicted reset. `expires_at == --at` and `reset_at == --at` are stale.

After those hard gates, retain a permitted current configuration when
`session_live && context_used_bps >= context_retention_bps`. At a 9000 threshold,
9000 retains; 8999 follows the otherwise eligible preference. A stopped session
has no live context to preserve. Its `keep` means keep the configuration only;
it does not mean the session is running or ready.

Every `switch`, including an effort-only change, sets `context_reset:true`.
Every `keep` sets it to false. This flag describes the recommendation's context
cost; it does not perform a reset or promise context transfer.

## Results

| `status` | Exit | Meaning | Action/target |
| --- | ---: | --- | --- |
| `recommended` | 0 | A policy-admitted `keep` or `switch` recommendation. | Present; `selected` is the catalog ID, with runtime/model/effort and `context_reset`. |
| `defer` | 3 | Known restrictions leave no eligible option, including no cheaper quota-clear included option. | Absent. |
| `insufficient_evidence` | 2 | Required relevant facts are missing, future or stale; `missing` identifies unavailable evidence when applicable. | Absent. |
| `refused` | 1 | Malformed input or known invalid/unknown configuration. | Absent. |

Results include `contract`, `status`, `task_id`, `rule_id`, a one-line `reason`
and `execution_authority:false`; failures also carry a `code` when applicable.
Do not extract a target from an unsuccessful result or treat a process failure
without a valid result as a recommendation.

The four files above at the shown instant illustrate this expected JSON result:

```json
{
  "contract": "router-recommend/1",
  "status": "recommended",
  "task_id": "EXAMPLE-001",
  "rule_id": "R06",
  "reason": "Current configuration remains preferred and within policy.",
  "execution_authority": false,
  "action": "keep",
  "selected": "standard-high",
  "runtime": "codex",
  "model": "model-standard",
  "effort": "high",
  "context_reset": false
}
```

Rule IDs distinguish invalid input (`R00`), missing evidence (`R01`), admission
correction (`R02`), quota (`R03`), combined admission/quota (`R02+R03`), context
retention (`R04`), preference switch (`R05`) and admitted current configuration
(`R06`). Admission includes the role allowlist, capability ceiling and included
billing; `R02` is not limited to ceiling violations. `R06` distinguishes a
preferred current tuple from a fallback keep in its reason. A clear-current-quota
`no_admitted_option` defer reports that no included configuration satisfies the
admission and quota gates, without claiming quota is the sole cause.
The example is a contract fixture, not an observation from a live consumer.

## Contract fixture cases

Starting from the four example files at the shown `--at`, change only the named
facts. These are expected contract outcomes to exercise against an implementation,
not reported test executions.

| Input delta | Expected outcome |
| --- | --- |
| None; stopped session | `recommended/keep`, `standard-high`, `R06`, reset false; configuration only. |
| Current model `model-large` | `recommended/switch`, `standard-high`, `R02`, reset true. |
| Pool-a usage 8001 | `recommended/switch`, `small-low`, `R03`, reset true. |
| Both pools' usage 8001 | `defer/quota_exhausted`, `R03`, no action/target/reset. |
| Remove `state.context_used_bps` | `insufficient_evidence/missing_input`, `R01`, missing that exact field. |
| Current model `unlisted-model` | `refused/unknown_configuration`, `R00`, no action/target/reset. |
| Live session, context 9000, small preferred before standard | `recommended/keep`, `standard-high`, `R04`, reset false. |
| Current model `model-large`, pool-a usage 8001 | `recommended/switch`, `small-low`, `R02+R03`, reset true. |
| Pool-a usage exactly 8000 | `recommended/keep`, `standard-high`, `R06`, reset false. |
| Live session, context 8999, small preferred before standard | `recommended/switch`, `small-low`, `R05`, reset true. |

## Classifier seam and validation

The default classifier is a deterministic rule table. A classifier is a linked
Go seam, not an executable selected by policy and not a dynamically loaded local
model. Its complete result must be postvalidated; it cannot bypass hard
admission or quota gates, invent a tuple, change the task binding, return a target
on a non-recommendation, or misreport the context-reset flag.

From a matching repository, import `relux.works/swarma-router/recommend`.
The linked API is:

```go
func Recommend(raw RawInputs, at time.Time, classifier Classifier) Result
// RawInputs contains Task, Member, State and Policy []byte.
type Classifier interface { Classify(Classification) Result }
```

Pass the original four JSON byte slices, the explicit evaluation instant and
`nil` to use `RuleTable{}`. `Recommend` owns strict decoding, missing/freshness
checks and hard gates; do not decode/re-encode first or call a custom classifier
directly as a substitute for that boundary. A zero evaluation time yields
`insufficient_evidence`. Outcome errors are `Result` values; use `ExitCode()` for
the mapping above.

Implement `Classify(Classification) Result` in Go and pass that value as the
third argument. `Classification` contains `TaskID`, `Current`, hard-gated
`Eligible` tuples, `PreferredTupleIDs`, `SessionLive`, context use/retention in
bps, and `RuleID`/`Reason` for forced admission/quota corrections. It is a detached
snapshot, not live source access or new policy authority. `Tuple` exposes the
catalog ID, runtime/vendor/model/effort, rank, relative cost, quota scope and
billing. Return a complete `Result`, including the exact contract/task binding,
rule, reason and `execution_authority:false`; recommended results also need the
selected ID, runtime/model/effort, action and a non-nil `ContextReset` boolean
pointer. Non-recommendations omit all action/target/reset fields. Invalid plugin
output returns `refused/classifier_invalid`, without trying a fallback candidate.

The IDs `R00` through `R06` and `R02+R03` are reserved. A forced correction must
preserve the supplied `RuleID` and `Reason`. Without a forced correction,
`R00`/`R01`/`R02`/`R03`/`R02+R03` cannot label a recommendation, and
`R04`/`R05`/`R06` must match the default table's complete decision and exact
reason. In particular, `R04` requires an actual live high-context retention
keep; a switch or a keep without retention cannot claim it. A classifier may
use its own rule ID and explanation to select another eligible tuple when
retention does not apply. Custom explanation text belongs to the classifier;
the engine checks its shape and the selected tuple's hard gates, action, reset
and binding, rather than verifying arbitrary prose.

The default table chooses a forced correction in eligible preference order,
then eligible catalog order. Otherwise it preserves high live context, selects
the first eligible preference, or keeps the current admitted tuple. Test a
classifier through `Recommend`, so hard gates and complete result checks run.

A plugin test needs both a valid recommendation and forbidden results: unknown
or disallowed tuples, above-ceiling targets, quota-exhausted targets, paid
options, effort-only switches without reset, and non-recommendations carrying
action/target fields. Check the returned status and absence of any forbidden
selection, not merely that an error occurred. Include the 8000/8001 quota and
8999/9000 context boundary pairs and expiry/reset equality cases.

From a matching source checkout with dependencies prepared, the focused commands
are:

```sh
GOTOOLCHAIN=local GOPROXY=off go test -count=1 ./recommend ./cmd/swarma-router
GOTOOLCHAIN=local GOPROXY=off go build ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
```

These commands require the actual engine and CLI packages, not just this guide.
Run both the valid controls and the intended refusals, and retain each lane's
exit even if another lane fails. Source tests qualify only the exercised
implementation and fixtures; real telemetry and real use need separate evidence.

Automatic model/catalog discovery, a local-model classifier, scheduling and
launch/application integration are later work. The read-only recommendation
call has no launch, messaging or environment-manager prerequisite.
