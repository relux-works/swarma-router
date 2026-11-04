# Partial task facts

`TaskFactsSource.Observe(ctx, nativeTaskID)` returns a `TaskObservation`: exact
`ID`, `Title`, `Status` and `Assignee` plus `ProjectionSHA256` for the captured
task-object bytes. Empty assignee and unfamiliar native status remain data.
The observation has no verified task revision, source time/expiry, target, gate
or readiness. Its digest identifies bytes and authenticates no producer.

`TaskBoardFacts` implements this read-only source for one explicitly admitted
local task. It does **not** implement `OwnerReader`; these four facts cannot
represent positive `OwnerInputs`. The planner remains unavailable without a
separately admitted full owner reader. A later composer needs actual verified
task/card/policy/mapping/custody and action-specific witnesses. A successful
lookup authorizes no preparation, assignment or launch. No workbench wire field
or command entry point is added.

## Trusted configuration

The embedding fixes the absolute `Executable`, `ProjectRoot`, `BoardDir`, one
`AdmittedTaskID`, public `Environment` and explicit `Runner` outside request
JSON. Configure before use and do not mutate concurrently. Use
`TaskFactsExecRunner{}` for real direct execution or an admitted synthetic
runner for tests; there is no implicit runner or source fallback.

The requested native ID must exactly equal the admitted one before commands.
This source does not assume that a Prepare task reference's `object` is a native
board ID, or verify its supplied revision. The embedding must separately admit
that mapping. Unsupported native quoting cannot narrow the public Ref grammar.

The environment is a positive list: `HOME` (the actual caller home), `PATH`
(nonempty absolute entries), and optional `TMPDIR`, `LANG`, `LC_ALL`. Values are
explicit public settings. The source forwards no ambient vector, task-board
selectors, messaging/native markers or credentials. The executor rechecks that
list, including duplicate-key rejection. A fixed absolute executable runs
directly, with no shell or executable search fallback.

## Read boundary

One observation performs at most two sequential calls, containing exactly one
task lookup:

```text
EXECUTABLE --board-dir BOARD_DIR q --format json 'project_config()'
EXECUTABLE --board-dir BOARD_DIR q --format json 'project_config(); get("ADMITTED-ID") { id title status assignee }'
```

The first local configuration check must succeed before any get. The second
batch rechecks that binding and supplies exactly one task object. Both reads
require effective/configured local mode, the exact project/board, local active,
remote inactive and no explicit remote override. Relative and absolute local
board-directory declarations that resolve to the same admitted directory are
legitimate. Unrelated configuration metadata cannot override the required exact
predicates. This is not atomic protection from hostile same-user configuration
replacement. Project configuration and executable custody remain trusted.

The query uses supported quoted positional `get`, with no named-argument retry.
An installed tool can return exit zero with a JSON error envelope; success
requires the expected source data, not only a process exit. There is no retry,
enumeration, alternate board, remote source or mutation.

A live deadline context is required. One shared 29s command budget is bounded
further by the caller deadline; pipe cleanup adds at most 1s. Each stdout is
bounded to 2MiB, stderr to 64KiB and the captured task object to 65536 bytes.
JSON nesting is bounded to 64 containers. Real children are waited on; the source
starts no background poller or scheduler. Synchronous parsing and configuration
checks honor context boundaries without a kernel-preemption guarantee.

## Projection and errors

The task object has exactly `id`, `title`, `status`, `assignee`, each a string.
The ID must match the admitted native ID. Missing/null/unknown/aliased/duplicate
keys, invalid UTF-8/scalar escapes, trailing values, malformed/truncated/oversized
data, absent task and error envelopes return no observation. Ordinary JSON
whitespace and equivalent valid escapes are accepted.

All decoded values are preserved without trim, normalization or rewriting.
`title` is the native full title, which may include the ID prefix; `name` is a
different slug and cannot substitute for it. No human-facing task message is
generated. `ProjectionSHA256` hashes the exact captured task object, including
its internal JSON spelling, separately from configuration and surrounding batch
bytes. It is not a canonical request digest, verified revision or allow gate.
The current supported projection supplies no revision; created/updated metadata
and local capture times cannot manufacture one or an owner receipt lifetime.

| Failure | Closed refusal |
| --- | --- |
| Different admitted native ID | `authority_denied` |
| Nil, canceled, expired or unbounded context | `authority_unknown` |
| Wrong task identity or local binding | `source_conflict` |
| Invalid configuration, missing/failed/malformed/oversized source | `source_unavailable` |

No raw command/configuration/stderr diagnostic is returned. A partial or failed
read yields zero facts, never reliable absence or partial authority. A future
supported revision-bearing source needs an explicit compatible contract;
unexpected fields never become authority implicitly.

## Validation

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./workbench -run '^TestTaskFacts' -count=1
GOTOOLCHAIN=local GOPROXY=off go test -race ./workbench -run '^TestTaskFacts' -count=1
GOTOOLCHAIN=local GOPROXY=off go vet ./workbench
```

Tests pair exact facts/empty assignee/Unicode/opaque status/size boundaries with
foreign IDs, configuration changes, invalid projections, exit-zero errors,
cancellation, environment refusal and real isolated subprocess output limits.
They assert one task get, fixed command binding and absence of the positive
OwnerReader bridge. These observations and fixtures establish no actual
selection, owner issuance, runtime readiness, policy install or live grant.
