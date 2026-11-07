# Stored association in the launch embedding

The internal `launchbridge` package composes the existing `launch.Router`,
`launch.Curator` and private launch journal. It adds no launcher, selection,
registry, enrollment or lifecycle operation. This is a source integration
boundary. Native integration is NOTRUN/unavailable until the separate source
command lands and the exact launcher handoff route is admitted and qualified.
Construction does not authorize running a member.

`AssembleAssociation` and `Prepare` are pure. The reused custody acquisition
reads exactly the configured membership and event through the admitted caller's
runner. It validates sender, recipient, event, room, topic, root, handover kind,
reply, complete intent/configuration/profile digests, access sets, explicit empty
holds and validity window. It reads no foreign credentials. The separately
admitted `OwnerSource` must still acquire current task/configuration/holds and
complete identity/native absence. Target absence alone is insufficient. Missing
owner source, live/uncertain target and expired custody refuse admission.
Acquisition rechecks expiry and clock rollback at completion; admission uses the
Router's acquisition-start time as its conservative check-time bound.

`AssociationReady` requires two separate trusted capabilities:

- `BindingAcquisition` independently obtains the current native runtime/thread
  and authenticated operation-bound binding/lane read. The adapter checks its
  task/member/session/pane, exact sender including server, full process witness,
  thread, freshness and private expected tuple. It also supplies the retained
  complete original intent bytes from admitted private policy. Those bytes must
  hash to the lower contract's digest; tuple-only, missing, changed or differently
  serialized bytes refuse before comparison. The adapter does not reconstruct
  original bytes or silently canonicalize an earlier transport. A declaration, process digest,
  test double or nonempty reference string does not authenticate its origin.
- `AssociationTransport` delivers the private frame exclusively to the already
  selected own-context child and returns bounded stdout, stderr and actual exit.
  There is no installed transport in this change, generic file/JSON fallback,
  status-command fallback or alternate credential selection. Nil is unavailable.
  An injected callback is a trust premise, not proof of native execution.

The private frame uses the existing closed schema
`a2a.terminal-association.proposed/1`, unchanged field spellings, the complete
`launch.Intent` digest from the lower contract's fixed JSON encoding, selected
own identity/connection assertions and all eight Target fields. It never
substitutes a tuple digest. Exact SessionID stays private. The comparison window
is at most 30 seconds and cannot outlive the independent witness. No Host,
origin assertion, authority flag or other wire field is added.

The adapter accepts only a closed object plus LF (at most 2048 bytes), exact
stored-only scope/source/no-native-evidence literals and correct exit/result/
reason coupling. Duplicate/unknown/case-alias/missing fields, malformed numbers,
partial output, stderr and changed correlation refuse. A match must also equal
expected generation, consumer and owner PID/birth digests and runtime. Local
collision defense includes decimal observation/PID formatting. Every refusal or
mismatch returns zero readiness. Private tuples, raw output and source errors
are never added to receipts or diagnostics.

## Source obligations

| Boundary | Required behavior |
| --- | --- |
| Origin and FD | The admitted launcher authenticates origin; descriptor metadata and source do not. Use anonymous private regular input, reader-owned 0600, zero links, read-only FD 3, bounded framing and exclusive inheritance. Linux requires the specified seals; cooperative non-Linux custody stays explicitly weaker. |
| Own context | Resolve selected identity and paths only. Do not call credential-loading session resolution, a secret store, connection checks or network. No lease/hook effect or fallback selection. |
| Single stored generation | Read selected own state under the existing lock, opened without creation. Binding Host equals local machine label; lease holder equals SessionID and lease expiry is after now. Another Host or a pid holder is stale. HostEnrollment is not interpreted. These source checks add no public Host field or Router native probe. |
| Bounds | Nonblocking state-lock acquisition: 100 ms maximum. Copy/decode under lock: 100 ms maximum. Whole source operation, input through output: 250 ms monotonic maximum. The adapter passes and checks the 250 ms deadline; it cannot independently measure source phases. Missing/unavailable/held lock gives snapshot_unavailable and no evidence, even if a writer normally holds it for seconds. Do not take the runtime control lock. |
| Birth digests | Exact stored-token hashes are low-entropy correlating declarations, not cross-host process identities or secrets. Conservative privacy collision refusal includes numeric observation formatting. |
| Dispatch and schema | Future fixed command: `a2a bind association --private-intent-fd=3 --json`, before connection preflight and owner-DM warnings, with no stderr. Target retains Socket, SocketID, Pane, PanePID, PaneBirth, OwnerPID, OwnerBirth and Runtime. Source state decoding tolerates unrelated top-level additions while validating the relevant binding strictly. |

Stored equality establishes neither attachment, native currentness, ancestry,
provider responsiveness, absence nor launch permission. Missing Runtime,
consumer death, recorded stop and target replacement do not establish family
exit or parent reaping. Independent acquisition supplies runtime/thread/lane
evidence; association output supplies none of those fields. The lower Curator
still checks the retained parent/birth/executable chain.

## Uncertainty and qualification

The lower journal persists UNKNOWN before its one Start. Matching replay
returns retained historical facts without another Guard or Start. Observe may
reconcile metadata to pending/running but never starts. Association unavailability
cannot clear uncertainty, change correlation or select another journal. A new
effect-free comparison window is distinct from a new launch intent.

Tests exercise the actual adapters, lower Router/Curator and private FileStore
with controlled source premises. Valid start/resume and both runtimes sit beside
invalid/missing/stale/misbound/refusal controls. Synthetic Host/lease rules and
real flock on private precreated fixture files check no-evidence behavior without
reading real source state. A delayed transport exercises the 250 ms bound.
Fixtures do not qualify native authentication, source comparator code, OS
cancellation, FD immutability, actual Host/lock interoperability or macOS custody.
Whole-candidate independent review, checks and release admission remain required
before use. This package performs no preparation-time native or provider reads.
