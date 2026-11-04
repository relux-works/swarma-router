# Current local policy access

`PolicyAccess` implements the existing workbench `Access` port. Its trusted
embedding fixes `LocalContext`, a current clock and one `PolicySource` outside
request JSON. `PolicySource.Decide` receives the exact operation tuple, one of
`PolicyRead`, `PolicyPrepare` or `PolicyObserve`, and the trusted clock callback.
It returns only `PolicyAllow`, `PolicyDeny` or `PolicyUnknown`; zero and unknown
values grant nothing. Sources must honor the bounded context, validate their
whole input and sample current time after acquisition. There is no source
selection, file path, clock, fallback or grant in the request wire.

`LocalPolicySource` is the first implementation. A future source can implement
the same router-owned interface without changing the engine or importing private
counterpart internals. The source must be independently admitted by the embedding;
an interface implementation alone does not authenticate a decision. No signed
grant source is implemented here.

## Trusted configuration

Configure these objects before use and do not mutate configuration concurrently:

```go
source := &workbench.LocalPolicySource{
    Path: "/absolute/private-directory/policy.json",
    OwnerUID: expectedOwnerUID,
}
access := &workbench.PolicyAccess{
    Local: workbench.LocalContext{Caller: caller, Project: project},
    Clock: time.Now,
    Source: source,
}
// The separately configured engine receives access through its Access port.
```

The example creates no policy or permission. The owner-managed file and parent
must already exist in trusted custody. Calls require a live context with a
deadline. Caller/project mismatch is `authority_denied` before source acquisition;
invalid context, configuration, correlation or clock is `authority_unknown`.
The clock callback is trusted synchronous code and must return promptly. The
file source samples it after reading and validating the policy, so a rule that
expires during acquisition does not retain its earlier validity.

## File format

The source is one UTF-8 JSON object, optionally surrounded by ordinary JSON
whitespace, followed by EOF. It is separate from request JSON+LF framing.
The maximum total file size, including whitespace, is 65536 bytes. The rules
array has at most 1024 entries; the byte bound can constrain it further. Empty
rules are valid and grant nothing. Every field shown below is required:

```json
{
  "contract": "swarma-router-local-access-1",
  "rules": [{
    "caller": "example-caller",
    "project": "example-project",
    "correlation_id": "example-operation",
    "read": "allow",
    "prepare": "deny",
    "observe": "allow",
    "valid_from": "2026-11-04T03:00:00.000Z",
    "expires_at": "2026-11-04T03:01:00.000Z"
  }]
}
```

Top-level and rule keys are closed, case-sensitive and unique after JSON escape
decoding. Unknown/missing fields, duplicate tuples, invalid UTF-8/scalar escapes,
trailing JSON and decisions other than `allow` or `deny` invalidate the whole
source, including bad nonmatching rules. Caller, project and correlation use the
existing broad Ref grammar and exact decoded equality, without normalization.
A literal `*` is an ordinary exact character, never a wildcard.

Times use canonical millisecond UTC spelling and `valid_from < expires_at`.
A rule is active when `valid_from <= now < expires_at`.

| Current exact rule | Access result |
| --- | --- |
| Active `allow` for this action | Success |
| Active `deny` for this action | `authority_denied` |
| Missing or inactive | `authority_unknown` |
| Invalid, unavailable, unsafe or changed source | `authority_unknown` |

## Read and revocation boundary

Each permission check independently reads fresh bytes. The existing file must
be a regular file owned by the configured UID with exact mode 0600; its immediate
parent must be owned by that UID with exact mode 0700. Special permission bits
are refused. Darwin additionally rejects extended ACLs and unknown ACL reads;
Linux uses the existing private-mode effective-permission predicate.

The parent is opened without following a final parent symlink. The final file
is opened relative to that retained directory descriptor with `openat` and
`O_NOFOLLOW`; a FIFO is opened nonblocking and then refused. Reads are bounded.
Opened-file identity, mode, size, modification/change times, owner/group and link
count are compared across the read and anchored name recheck. Parent identity
and current privacy are checked again. An observed replacement or mutation
during this boundary is unknown. An independently completed replacement between
calls is legitimate and evaluated afresh, including revocation before Start.
Ancestors and the configured path remain in trusted custody.

The adapter performs no create, chmod, repair, migration or alternate-source
lookup. Synchronous filesystem calls check context around operations; a deadline
does not preempt a blocked kernel call. This is local single-user attenuation,
without hostile-same-UID protection, a protected clock, or atomic revocation
after the final check. No policy bytes, path or diagnostic text reaches public
failures. `LocalPolicySource.LastRead()` exposes only the last successful stable,
validated read's SHA-256 and checked time to trusted process evidence. It is not
a cached permission, and it can describe an expired or explicitly denying
policy. It remains historical after a later failed read.

The engine's existing phase mapping is unchanged. Inert historical Prepare
replay needs current Read only. New Prepare checks Prepare before source work
and rechecks it before Start. Status checks Observe before observation. Prepare's
original reuse observation retains its existing Prepare permission mapping.
Local grants supply no task/card/profile/quality/custody/absence/readiness facts
and never authorize a new target or prove a usable runtime.

## Validation and dependency

The module targets macOS and Linux. Anchored Darwin `openat` uses
`golang.org/x/sys v0.35.0`, requiring Go 1.23.0 or later. Populate the module cache
once with `go mod download`; bounded offline checks are:

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./workbench -run '^(TestLocalPolicy|TestPolicySource)' -count=1
GOTOOLCHAIN=local GOPROXY=off go test -race ./workbench -run '^(TestLocalPolicy|TestPolicySource)' -count=1
GOTOOLCHAIN=local GOPROXY=off go vet ./workbench
```

Tests use isolated synthetic files, a synthetic interchangeable source and the
real engine with other owner/lifecycle ports injected. They cover exact decisions,
read-only replay, denied observation, unsafe files/parents/ACLs, schema and time
boundaries, cancellation, stable-read changes and second-Prepare revocation.
They establish no installed policy, live grant, production owner acquisition,
launch qualification or complete runtime wiring.
