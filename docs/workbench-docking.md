# Workbench component docking

The `workbench` package can compose its parser, operation engine, source planner,
deterministic selector, private file repository and lower lifecycle adapter with
the existing `launch.Router`, launch file store and Curator plugin. The embedding
chooses fixed trusted ports and separate private upper/lower directories. Request
JSON cannot choose either directory, a plugin, executable, callback or identity.

`workbench/docking_test.go` exercises that complete code path. Its backend Create
only increments an in-memory counter and records a process snapshot. Owner,
runtime and readiness receipts are fixtures; no Curator, tmux or native runtime
is executed, and no task or message is posted. These tests establish component
compatibility under the admitted port contracts, not actual source authenticity,
runtime liveness or qualification of a live tuple.

## Retained behavior

An admitted stopped target retains the exact upper request and lower intent in
their respective stores before the fixture's one Create. The first response is
pending. Original-key Status observes the existing lower operation and requires
both current lower evidence and a separate complete upper witness before ready.
Ready reuse does not open a lower store or call a native backend.

Reopening the upper repository preserves the original record. Matching Prepare
is an inert historical read without source resolution, lifecycle calls or record
mutation. Changed canonical content under the same key conflicts; Status of an
absent key neither reserves nor observes another operation. The original key
continues to reconcile normally.

Missing, expired or misbound upper evidence cannot become ready from lower
running alone. The checks retain original generation, runtime birth identity,
operation digest, task and messaging route. Owner task-state refusal is tested
as a fixture port response, not as implementation of production task acquisition.

Busy or corrupt upper/lower storage is uncertainty, never an invitation to reset,
reselect or repeat Create. Injected acknowledgement loss after actual file writes
and the one fixture Create reconciles through fresh stores and original-key
Status. The source snapshot's own capacity does not guarantee that the encoded
aggregate operation fits: an oversized aggregate is stopped before lower mapping
or effects, while the nearby one-target positive control remains usable.

## Validation and remaining work

```sh
go test ./workbench -run '^TestWorkbenchDocking' -count=1 -v
go test ./...
go vet ./...
go test -race ./...
```

The source runs on macOS and Linux. Cross-compilation alone checks compilation,
not Linux filesystem behavior. Separate file repository tests cover custody,
CAS, durability steps and cooperating process exclusion. Combined tests use local
temporary directories and do not simulate machine power loss or a hostile
same-user writer. Logs and isolated controls belong under `.temp/`.

An isolated removal of the lower-result digest check must retain valid readiness
and fail the docking wrong-result assertion. That control checks this test's
ability to detect one binding violation; it is not exhaustive mutation coverage.

Production owner receipt acquisition, actual task/custody/configuration and lane
verification, a production Host source adapter, complete runtime wiring and a
workbench CLI remain unimplemented. A caller still needs admitted actual sources,
authority and a qualified useful member tuple before any real effect. Component
docking does not qualify delivery, adoption or live use.
