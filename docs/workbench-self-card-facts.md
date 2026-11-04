# Partial self-card declarations

`SelfCardFactsSource.Observe(ctx)` returns four desired self declarations and
the selected card's byte digest. It does not implement `OwnerReader` or supply
current eligibility, installed configuration, runtime state or launch authority.
The source is a separate Go port; no public wire operation or CLI is added.

`SelfCardObservation` contains:

- `Facts.MemberKey`: the exact qualified card/member key.
- `Facts.ResolvedName`: the complete declared Matrix name, including its server.
- `Facts.Role`: the declared role.
- `Facts.Project`: the declared qualified project, or nil when undeclared.
- `SourceSHA256`: the exact selected card-byte SHA-256.

Profile and runtime declarations are outside this projection. There is no
verified revision, source observation time, expiry, target or gate. A content
digest is not a freshness or authority receipt. The underlying source remains
cryptographically unverified under single-user local trust.

## Trusted source selection

Configure `PulseSelfFacts` out of band with an admitted absolute executable,
working directory, original absolute in-place card path, independently checked
lowercase SHA-256, actual public caller, expected member/full name/role/nullable
project, explicit public environment and synchronous runner. Configure once;
do not mutate concurrently. No request can select these fields, and there is
no tool search, source discovery, retry, default runner or fallback view.

The source performs exactly one direct invocation:

```text
PULSE_ABS me --local-unsigned --card CARD_ABS --revision sha256:PIN --format json
```

It uses the default `pulse-view/v1` self query. It never selects another member,
changes the caller, requests environment/discovery views, follows card references
or opens the referenced profile, configuration or working-root paths.
The independently admitted Pulse executable validates the complete supplied v3
card, including its supported legacy extensions and aliases. The adapter does
not replace that validator with a narrower parser.

The child's positive environment allows actual `HOME`, absolute-entry `PATH`,
optional absolute `TMPDIR`, `LANG`/`LC_ALL`, and exactly one `A2A_AGENT` matching
the existing public caller. Other selectors, credentials and native runtime
markers are rejected. No ambient environment vector is copied. This is a
separate runner policy; the task-facts runner continues to reject `A2A_AGENT`.
Single-user trust does not defend against a hostile same-user process changing
the environment, admitted executable or source files.

## Validation and refusals

The context must already have a deadline. Execution is capped at 29 seconds,
with at most one additional second for pipe cleanup. Stdout is bounded to 2 MiB
and stderr to 64 KiB. The transport can capture an exact-capacity diagnostic,
but **every nonempty stderr refuses the observation**. Neither an error nor a
refusal returns partial facts or native diagnostics to the observation caller.

Require exit zero and one complete valid UTF-8 JSON document. Duplicates,
invalid scalar escapes, nesting beyond 64 containers, aliases of interpreted
fields, wrong types, trailing/truncated data and error envelopes refuse.
Unrelated valid v1 metadata is accepted and omitted from the returned projection.
Require the exact schema/mode, caller/unverified trust markers, member/key/full
name/role/project consistency and source schema/pin/revision/original card parent.
Source freshness must remain `unknown`, with `observed_at: null`.
Query time is never promoted to source freshness.

| Condition | Closed refusal |
| --- | --- |
| Missing, expired or canceled bounded context | `authority_unknown` |
| Missing or changed actual caller | `authority_denied` |
| Unavailable trusted source, runner, configuration or failed subprocess | `source_unavailable` |
| Conflicting source identity/content/shape, output bounds or diagnostic stderr | `source_conflict` |

The nullable-project case preserves a declared absence; it does not infer a
project from a keeper's location. Source failure is never reliable member
absence, available capacity or positive ownership.

Tests cover declaration preservation, exact command/context/environment,
neighboring conflicts, generic JSON integrity, real subprocess limits and
cancellation. These isolated tests do not qualify an arbitrary executable/card
pair, current appointment, installed profile, runtime or live launch tuple.
