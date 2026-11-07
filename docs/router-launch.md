# Explicit launch API

The Go package `relux.works/swarma-router/launch` implements the reduced
`router-launch/0.1.0` contract. A manager supplies an already selected member,
runtime, profile, model and effort. Router validates that intent, records one
attempt and delegates to an explicitly configured plugin. It does not select a
worker/model, assign a task, implement the proposed full Route/Apply interface,
provision an identity or stop a session. There is no launch CLI in this slice.

The implemented plugin invokes Curator in a named tmux session. A future session
host or dispatcher integration needs its own plugin and qualified contract.

## Entry points and trust

```go
plugin := launch.Curator{Config: admittedConfig, Backend: admittedBackend,
    Ready: ownerReadinessSource, Now: clock}
router := launch.Router{Manager: manager, Project: project, Plugin: plugin,
    Guard: ownerAdmissionSource, Now: clock,
    OpenStore: func() (launch.Store, error) {
        return launch.OpenFileStore(privateLaunchJournal)
    }}
intent.Plugin = plugin.ID()
intent.PluginConfig = plugin.Configuration()
prepared := router.Prepare(intent)
receipt := router.Start(ctx, intent)
current := router.Observe(ctx, intent)
```

These are library integration fragments, not runnable authorization examples.
The embedding application implements trusted `Guard` and `Ready` adapters. An
arbitrary request boolean or unverified JSON document is not either adapter.
`SystemBackend{Runner: ExecRunner{}}` is the concrete tmux/process adapter;
injected backends and command runners support isolated tests.

`Intent` binds contract, correlation ID, project, task ID, member, manager,
plugin ID and configuration digest, runtime, profile and profile lock, model,
effort, workspace, tmux session, start/resume mode and native session reference.
All fields participate in the SHA-256 digest of the fixed Go JSON struct
encoding. This is a distinct contract, not the full interface's canonical JSON
format. The profile lock and plugin configuration digests are 64 lowercase hex
characters. Runtime is `codex_cli` or `claude_code`. Names and arguments use
bounded positive grammars; tmux names contain only letters, digits, hyphens and
underscores and begin with a letter or digit. Arbitrary argv/env and leading option injection are
not accepted. Workspace is an absolute clean path, and the concrete backend
requires its real directory path without symlink ancestors.

`CuratorConfig` pins the executable, real user home, PATH, temporary directory,
permission mode, recorded native-fallback refusal reference, optional network
selector and optional target member identity selectors. `Configuration()` binds
all these fields. It does not hash executable/profile contents; their custody
and selected versions remain the embedding manager's responsibility.

`Guard.Check` must verify the real task, selected member/card, profile lock,
runtime/model/effort, workspace, permissions/network selection, current holds,
assignment authority and exclusive session custody. Its admission must match
the exact intent digest, manager, member and profile lock, with valid current
timestamps and `State="stopped"`. Stopped requires both no live member session
and no live identity binding, or the admitted native-member equivalent. Unknown
is insufficient. Router rechecks expiry/cancellation before the effect call;
the plugin independently refuses an existing or uncertain tmux target.

One canonical session name and exclusive member custody are manager invariants.
Local locking is not distributed fencing against another manager, another
journal or an adversarial same-user process. No capacity/load gate is implemented
for this reduced route.

## Curator translation

Normal preparation produces:

```text
CURATOR run RUNTIME --native --profile PROFILE --model MODEL --effort EFFORT --permissions yolo
```

An explicit network selector adds `--network SELECTOR`. Its omission does not
disable Curator's global network admission. Curator retains its own profile,
policy, credentials and runtime admission checks. A profile lock is verified by
the trusted admission source; Router does not invent a Curator lock flag.

Only a configured `permissions="native"` with a recorded yolo-refusal reference
adds the runtime's native full-permission flag after `--`. There is no automatic
fallback or second invocation. Resume adds `resume SESSION` for Codex or
`--resume SESSION` for Claude after `--`, preserving the selected profile.

The tmux command uses direct arguments, not a shell command string, `-A`, terminal
input or an adoption/replace operation. The child starts with `/usr/bin/env -i`
and only configured HOME, PATH, TMPDIR, LANG and target A2A_CONFIG_DIR/A2A_AGENT
selectors. Parent runtime homes, session markers, identity and credentials are
not inherited. The Router never switches its own messaging identity to obtain
readiness. The configured home must be the actual admitted user's home, not a
fabricated environment to bypass Curator provenance checks.

The concrete backend uses the caller's existing tmux server selection. A
successful filtered session listing can establish target absence. An unavailable
server is unknown, even if starting a new server might otherwise be possible.
It does not create a server as an absence probe. Metadata traversal is bounded
to 32 processes and 10 seconds; individual tool calls have bounded output and a
29-second execution deadline plus a one-second pipe-close delay. A timeout of
the tmux client does not prove cancellation of a member it may have created.

## Attempt versus readiness

`Prepare` is pure translation: no admission query, journal, session or process
effect. `Start` records an attempt; a complete receipt includes the token-free
command, profile/lock, operation digest, session/pane, pane PID and actual
`curator-run` PID with parent, executable and birth witness. This is `attempted`,
not positive liveness. Missing startup metadata remains unknown. There is no
polling daemon or second start to compensate for slow startup.

`Observe` inspects the same operation/session/pane and a bounded process tree.
It requires a `curator-run` descendant of the pane and, for readiness, an
unambiguous `codex` or `claude` descendant. Retained PID/parent/birth/executable
witnesses must continue to match. Unknown or changed identities remain unknown.
This process-name compatibility must be qualified on the actual runtime tuple.

The injected `Ready` adapter authenticates fresh task/member/session/pane/runtime
evidence. For a chat member it must independently obtain the exact binding
pointing at that runtime/thread, its answer and the member's first lane post.
The library checks matching runtime birth witnesses and binding/thread/lane
references with the exact sender. For a non-chat member it requires the current
native assignment and durable handoff instead. Authentication of those sources
belongs to the adapter. Without a readiness source, process metadata can yield
`pending`, never `running`. The internal [association embedding](router-launch-bridge.md)
adds a stored-only comparison gate around independently acquired readiness.
Its native command/transport remains unavailable pending separate source landing
and qualification; no installed production binding reader is shipped here.

| Status | Meaning |
| --- | --- |
| `prepared` | Pure translation; no effect. |
| `attempted` | Complete attempt receipt; liveness unverified. |
| `pending` | Process metadata observed; independent readiness incomplete. |
| `running` | Separate observation passed current owner-source readiness checks. |
| `refused` | No new plugin effect attempt in this invocation, or an explicitly retained historical refusal. |
| `conflict` | Existing correlation belongs to another intent. |
| `unknown` | Possible effect or unresolved observation/durability. |

A matching Start replay has `Historical=true`; it reports retained facts and
does not assert current liveness. No status means task acceptance or completion.
Ended-state proof is not implemented; absent sessions conservatively remain
unknown. Cleanup remains with the manager through its existing safe session
exit/process-absence procedure. Router supplies no watchdog, kill or stop path;
Curator execution alone does not guarantee complete descendant cleanup.

## Durability and recovery

Use one explicitly owned private **launch** directory for these operation keys,
separate from all queued-notice journals. The file store reuses the private
directory/ACL, parent-fsync and nonblocking lock primitive; its distinct
`launch-*.json` records neither interpret nor migrate queued-notice receipts.
Records are limited to 64KiB and use strict decoding and validated bindings.

Router holds the lock and fsyncs an unknown attempt marker before calling Start
once. Matching replay returns the retained historical receipt after durability
confirmation, with zero plugin/admission source calls. Changed intent conflicts.
Unreadable, corrupt, concurrently locked or uncertain durable state prevents a
new attempt. Pre-effect persistence failure refuses that invocation; a visible
uncertain marker on a later call remains unknown. Post-effect save failure,
lost replies and malformed metadata retain uncertainty and known attempt facts.
`Observe` reconciles the same operation and never calls Start.

Preserve uncertain records and the original configuration. Do not delete/reset
them, change correlation, use a new journal or launch elsewhere to retry. The
library does not provide exactly-once execution after journal loss or outside
the manager's custody guarantees.

## Validation scope

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off go test -race ./...
go doc relux.works/swarma-router/launch
```

Tests pair admitted controls with invalid input/configuration, missing or stale
authority, cancellation/expiry, existing/unknown sessions, wrong process or
readiness binding, replay/conflict, lost reply, lock/custody and durability
failures. The Curator trace proves exact command/environment translation through
an isolated backend; no test launches a real member. Passing fixtures and a
cross-platform compile do not establish real Curator/provider execution,
live binding delivery or target-host adoption. Qualify those separately before
using the effectful entry point on an actual member.
