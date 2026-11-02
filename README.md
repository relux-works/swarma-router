# swarma-router

Chooses who runs a task and on what model. The router takes a board task together with the member's card and the
worker's current state: agent, context, limits. A small, fast local classifier weighs the available vendors and models,
the usage thresholds, the schedule of desired runs and the preferences by project and work type. It knows that a model
or vendor switch resets the context, and it recommends whether to keep the worker as it is or move it to a simpler or
a stronger model, with lower or higher reasoning. Managers assign tasks through it and adapt their workers at runtime.

The target design is an independent module connecting to the model catalog, the session host's usage events,
the board and product cards through plugin seams. The MVP currently links one combined CLI adapter.

Status: the full routing/classification interface remains a design. The reduced
`queued-task-handoff/0.1.0` Go implementation posts an explicitly authorized task notice.
Qualification on 2026-10-31 covers one Darwin arm64 plan, dispatch, historical receipt replay
and server-readback tuple. Automatic Route, Apply and model selection remain unimplemented.
The separate `router-launch/0.1.0` Go library translates an explicitly admitted launch through
a Curator plugin and retains one attempt. Its isolated tests do not qualify a live launch tuple.
See [the launch API](docs/router-launch.md) for trusted admission, observation and recovery contracts.

The separate `workbench` package parses the explicit-effort local
`swarma-router/0.2.0-draft.2` Prepare/Status request subset and computes canonical
Prepare identity. It checks recovery echo capacity before any operation can be
opened. See [the request boundary](docs/workbench-wire.md).
Its [retained request binding](docs/workbench-retained-request.md) validates the
original canonical request and lookup tuple separately from lifecycle state;
it is a value codec, separate from lifecycle state. The
[operation engine](docs/workbench-operation.md) reserves before source resolution,
retains selection and lower mapping, and invokes Start at most once under an
admitted serialized repository. Historical Prepare replay is inert; Status only
observes the retained operation. Production storage, selection/source/launch
adapters and a workbench CLI remain unimplemented. Isolated injected-port tests
do not establish actual readiness, crash durability or a qualified live tuple.

The [module skill](agents/skills/swarma-router/SKILL.md) documents the implemented queued-notice
interface and the proposed full routing/plugin interface. Skill packaging and environment adoption
retain their required validation and installation gates.

## Queued task handoff

The local CLI validates an existing task and an explicit destination, saves a durable intent, posts
through the caller's existing a2a identity, and checks the returned server event. A verified result
means **queued notification**. Recipient acceptance, work started, execution, liveness, assignment,
claims and exactly-once external delivery are separate facts.

The implementation targets macOS and Linux, uses Go 1.23 or later and the standard library, and needs
the already configured `a2a` and `task-board` CLIs. It creates no identity, room, session or daemon.
Current review, release and real-use qualification must be checked before adoption.

```sh
mkdir -p .temp/bin
GOTOOLCHAIN=local GOPROXY=off go build -trimpath -o .temp/bin/queued-task-handoff ./cmd/queued-task-handoff
GOTOOLCHAIN=local GOPROXY=off go test ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
```

Copy the two files in `examples/queued-handoff/` to an ignored local directory. They contain
illustrative references and will refuse until you supply actual admitted values. The policy is an
operator-controlled local authorization file. Keep it owned by the current user with no group or
other access (`chmod 600 POLICY.json`). On macOS, extended ACLs on the policy, journal, lock
or receipt are refused even with private mode bits. Ancestors must remain in trusted custody;
permissions and path checks do not protect against a hostile same-user process swapping paths. Admit only nonsecret task titles/references intended for the
selected room. No free-form task body, commands, credentials or identity override is accepted.

The policy binds:

- `project` to `project_root` (the absolute project directory) and `board_dir` (the absolute local
  board **storage** directory, usually `project_root/.task-board`);
- `server`, the caller's own `sender`, and the exact admitted `room`/`room_id`/`topic`/`reply_to`;
- each allowed task ID and its exact native `title`, including the board's existing ID prefix,
  to an explicit list of allowed recipients.

This policy does not prove or change a task assignment. Before a new dispatch, the adapter checks
the actual local board configuration and task ID/title, a complete joined-members projection
including the current caller and recipient, and the existing reply event's room, topic and joined
sender. It rejects unsupported or nonlocal board authority. Board child selectors `TASK_BOARD_*`
and `TB_*` are scrubbed from that child's environment; each read specifies `--board-dir` and JSON
format. The caller must control the local configuration and trusted executable search path. This
is not a boundary against an adversarial same-user process changing tools/configuration mid-call.

The logical `room` remains part of the request/policy binding. Every messaging call selects the
policy's exact `room_id`, so it does not depend on an alias existing on the caller's current
homeserver. Membership and event readbacks must still return that exact ID. This selection does
not change the wire schema, policy digest or historical receipt handling.

Set request `action` explicitly to `plan` first:

```sh
.temp/bin/queued-task-handoff --request REQUEST.json --policy POLICY.json --journal /absolute/existing/parent/handoff-journal
```

`plan` performs read-only source checks and returns a prepared notice. It sends zero messages and
does not create a journal. To authorize the actual notice, set `action` to `dispatch` and invoke the
same command. The journal's parent must already exist; use a real path without symlink ancestors.
The CLI creates a private leaf directory and holds a nonblocking filesystem lock through dispatch.
Keep one journal directory for the lifetime of these correlation IDs.

| JSON status | Exit | Meaning |
| --- | ---: | --- |
| `planned` | 0 | Source checks passed; notice prepared; zero messages sent. |
| `queued_notification` | 0 | Exact server event verified, or its historical verified receipt replayed. |
| `refused` | 2 | Request/source/custody validation failed before any new post attempt. |
| `conflict` | 2 | Correlation ID already belongs to a different intent/policy; no new post. |
| `unknown` | 3 | An existing/new attempt may have posted; no safe conclusion about delivery. |

Every JSON result carries `contract`, `status` and `correlation_id` (empty when intake fails before
correlation validation); `event_id`, `notice` and a bounded
`reason` appear when applicable. Native subprocess stdout/stderr is never echoed as an error.
Closed JSON fields require their exact case-sensitive spelling, including nested objects.
Open source projections allow unrelated metadata but reject aliases of recognized fields.
Unknown/duplicate JSON keys, trailing values and nesting beyond 64 containers are rejected.
Task titles reject control/format characters, line/paragraph separators, private-use characters,
surrogates and Unicode noncharacters to prevent hidden or reordered notice fields. Input/journal files are limited to
64KiB, policies to 256 tasks and 64 recipients per task. Each subprocess has a 30s deadline,
2MiB stdout and 64KiB stderr limits. Pre-send failures refuse; post/readback ambiguity is unknown.

## Recovery and rollback

The intent is atomically saved and fsynced **before** the post. A matching verified replay returns
the existing historical receipt without sending, after confirming its file and directory durability.
Every journal open syncs its parent, including recovery from an incomplete creation. A failed
receipt durability confirmation remains unknown with its known event ID and never reposts.
A changed request or policy under the same
correlation conflicts. Keep one immutable private policy snapshot per task and authorization revision;
new tasks use new policy files with the same journal. Retain each original request and policy for
replay and reconciliation. Even adding an unrelated task to an old allowlist changes its digest and
blocks reconciliation until the original policy is supplied. There is no implicit digest migration.
Historical replay is not fresh membership, authority or task-state evidence.

If intent rename succeeds but directory sync fails, the original `refused` / `intent_not_durable`
result establishes that invocation made no post. A later replay of its retained intent is conservatively
`unknown`; keep the original result with the journal for external reconciliation.

Missing/malformed CLI JSON or abnormal termination is not proof of refusal or no delivery, regardless
of the numeric process exit. Preserve the journal and treat the invocation as uncertain.

An intent without a returned event ID may represent a crash before the call or a successful send
whose result was lost. Replaying it stays unknown and does not post. If an event ID was durably
saved, matching replay only reads that event and can complete verification. A wrong binding or
unavailable readback remains unknown. After effects, a journal write failure remains unknown even
when the current output can provide a known event ID.

Preserve the journal and any returned event ID for external reconciliation with the existing
messaging custodian. The CLI has no force-retry, cancel, import-event or reset operation. Never
delete/edit an uncertain record, change the correlation key, copy to a new journal or use another
journal directory as a retry. Local locking cannot prevent duplicates after journal loss or use of
another directory, and cannot provide server-side exactly-once guarantees.

Rollback stops future CLI invocations and restores the prior reviewed binary through its normal
local artifact path; retain the journal. It does not retract an already published notice or undo
recipient activity. No service restart, session stop, identity change or board mutation is needed.

## Tools

| Tool | Use and entry point | Outputs |
| --- | --- | --- |
| Go | `go build -trimpath -o .temp/bin/queued-task-handoff ./cmd/queued-task-handoff`; `go test ./...`; `go vet ./...` | Binary and task-scoped logs under `.temp/`; ordinary Go cache. |
| gofmt | `gofmt -w handoff launch workbench cmd/queued-task-handoff` | Formats Go source in place. |
| task-board | Adapter uses `task-board --board-dir STORAGE q --format json 'project_config()'`, then `project_config(); get(TASK-ID) { id title }` | Read-only local configuration/task projections; no board mutations. |
| a2a | Adapter uses `a2a room members ROOM_ID --membership join --json`, `a2a read ROOM_ID --event EVENT --json`, and explicit `a2a post ROOM_ID --topic TOPIC --kind notice --to MEMBER --re REPLY NOTICE` | Existing messaging service plus local durable handoff receipts. |
| Curator | Launch plugin prepares `curator run RUNTIME --native --profile PROFILE --model MODEL --effort EFFORT --permissions MODE` | Explicitly admitted member environment; launch attempt metadata. |
| tmux | Launch backend uses `new-session`, filtered `list-sessions`, `show-environment` and `display-message` | One named session and bounded process metadata; no terminal input. |
| ps / pgrep | Launch backend uses `ps -o pid= -o ppid= -o lstart= -o comm= -p PID` and `pgrep -P PID` | Bounded process ancestry and PID birth witnesses. |

The [implemented seam reference](agents/skills/swarma-router/references/plugins.md#implemented-mvp-seams)
describes the Go interfaces and the exact CLI output expectations.

Tests use injected adapters, real subprocess fixtures and isolated filesystem controls. They exercise intended
refusals, message counts, locking, corruption, durability failures and post/readback loss. They do
not establish actual peer delivery, source admission or callable full routing conformance.

Owner direction, 2026-11-02: «нужно встраивать лаунч систем или чо там по буку - диспатчер».
The initial launch boundary uses Curator directly; an external dispatcher and a session-host plugin
remain separate integrations.

Owner direction, 2026-10-30: «да, нам нужен будет еще модуль который будет понимать чо за задача и какие модели каких
вендоров доступны / так же будет иметь расписание желаемых запусков по времени - скедулинг / возможно предпочтения по
проектам и типам работ на кого спавнить , а также какие пороги / аля сварма роутер / надо на эту тему бук свармы
полистать / им будет пользоваться руководитель чтоб адаптировать своих работников прям рантайм если надо / типо
переключить на модель попроще или наоборот пожирней и ризонинг поднять / это уже более серьезное решение будет чем были
спавны в борде / здесь будет локальная модель какие там щас шустрые и пиздатые модели-классификаторы  желательно не
сильно жирные) / т.е в него будет вгружаться задача со всем чо есть типо какой щас агент, какой у него контекст , а
классификатор будет смареть на ограничения свои , доступные модели, втыкать что пнреключение модели - сброс контекста
равно как и смена вендора и принимать взвешенное решение чо делать с этим воркером менять или оставлять / и так
руководитель будет раскидывать задачи на работяжек через роутер с аздачей с борды и идентикой-карточкой сотрудника )».
