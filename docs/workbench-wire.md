# Local workbench request boundary

`workbench.Parse(frame, local)` is a pure Go request parser for
`swarma-router/0.2.0-draft.2`. It accepts complete byte slices and returns an
immutable parsed request or a closed error code. It opens no file, source,
journal, socket or process. An embedding must bound and complete its input read
before passing a frame; this API does not provide transport deadlines or EOF
observation. There is no workbench executable or operation engine yet.

## Input and context

A frame is one UTF-8 JSON object immediately followed by LF and EOF, at most
65536 bytes including LF. JSON whitespace before the object and within it is
insignificant. Additional frames, trailing whitespace after the object, CRLF,
BOM, invalid UTF-8, lone surrogate escapes, duplicate decoded keys, unknown
fields and more than 12 nested containers are rejected. Paired surrogate
escapes, literal U+FFFD and equivalent valid escape spellings are accepted.
Arrays have at most 64 entries; configurations have at most 16. The initial
schema has no numeric application fields, so all numeric values are rejected.

`LocalContext{Caller, Project}` comes from trusted local configuration separately
from the frame. The request's caller/project must equal it. This establishes
local equality only; it does not authenticate a hostile process under the same
user, verify references, authorize effects or establish current readiness.
Request strings never select a path, executable, configuration, credential,
socket, target identity, arbitrary argv or environment.

Common types:

- `Ref`: nonempty opaque UTF-8 string, at most 256 encoded bytes. Unicode,
  whitespace and JSON-escaped controls are legitimate representation forms.
- `Digest`: exactly 64 lowercase hexadecimal SHA-256 digits.
- `RevisionRef`: exactly `{source:Ref, object:Ref, revision:Ref, digest:Digest}`.
  Equality retains all four fields. A valid digest is not source authentication.

Every field below is required, uses exact case, and is non-null. Objects are
closed at every level.

| Object | Fields |
| --- | --- |
| Prepare | `contract`, `operation`, `correlation_id:Ref`, `caller:Ref`, `project:Ref`, `task:RevisionRef`, `cards:CardRef[]`, `constraints:Constraints` |
| CardRef | `member:Ref`, `card:RevisionRef` |
| Constraints | `policy:RevisionRef`, `objective:Ref`, `configurations:Configuration[]` |
| Configuration | `member:Ref`, `runtime:Ref`, `vendor:Ref`, `model:RevisionRef`, `effort:Ref`, `profile:RevisionRef`, `profile_lock:Digest`, `placement:Ref` |
| Status | `contract`, `operation`, `correlation_id:Ref`, `caller:Ref`, `project:Ref`, `expected_request_digest:Digest` |

`contract` is the version above. `operation` is `prepare_workbench` or `status`
according to the schema. Cards contain 1–64 entries unique by member; two card
revisions for one member are invalid. Configurations contain 1–16 entries unique
by their complete canonical record. Each configuration member occurs in cards.
Multiple distinct configurations for one member are valid alternatives. Array
position is not a preference rank. Runtime/model/profile strings still require
actual admitted mappings outside this parser.

The subset requires explicit nonempty effort. Broader catalog `effort=null`
forms remain unavailable here; they must never become invented defaults.
Status cannot supply task, card or configuration overrides, retry, change or
another target. No free-form task body is accepted.

## Identity and recovery capacity

The parser normalizes cards by member UTF-8 byte order and configurations by
canonical full-record byte order, after rejecting semantic duplicates. It then
recursively sorts object keys by unsigned UTF-8 bytes and emits compact JSON.
Quote and backslash use their two-character escapes, controls U+0000–U+001F use
six-character lowercase `\u00xx`, and other Unicode is emitted directly. There
is no Unicode normalization or HTML escaping. Canonical bytes exclude LF.

`Request.Digest()` is SHA-256 of the entire canonical Prepare, including contract,
operation, caller/project/correlation, every complete revision and all
constraints. Key order, whitespace, array set order and equivalent escapes do
not change it. Changing an application field does. Status returns the supplied
original expected digest and no canonical Prepare bytes; it never computes a
second operation identity.

`Request.Key()` is the tuple `(trusted caller, trusted project, correlation_id)`.
It has no operation-kind suffix or ambiguous delimiter concatenation. Existing
notice and launch journal formats are unaffected. A future operation engine
must bind this key to its own admitted store and preserve original uncertainty;
the parser does not create or load an operation.

Before returning a request, the parser encodes a complete minimal recovery
envelope using the exact echoed key and digest, null optional operation/revision/
lower references and the longest closed reason. Top-level and nested key/digest
echoes count in full. The encoded JSON plus LF must fit 4096 bytes; otherwise
`output_budget` returns an empty Request. Long ordinary Unicode Refs remain
valid, while several control-heavy Refs may exceed this aggregate budget.
There is no ASCII-only substitute, truncation or dropped identity.

`RecoveryTemplate()` returns a copy of that sizing envelope. Its longest reason
is a sizing placeholder, not a real operation outcome. A future response encoder
must validate the actual response union, actual reason and 131072-byte output
budget, fit the 262144-byte retained record, and preserve the independent
4096-byte recovery bound before effects. This initial capacity check alone
proves none of those later engine obligations.

`Prepare()`, `CanonicalPrepare()` and `RecoveryTemplate()` return independent
copies. The zero Request has no valid operation. Parse errors expose only
`invalid_request`, `unsupported_version`, `authority_unknown`, `caller_mismatch`,
`project_mismatch` or `output_budget`; rejected values are not echoed.

## Verification

Run with Go 1.23 or later on a supported macOS or Linux host:

```sh
GOTOOLCHAIN=local GOPROXY=off go test ./workbench -count=1
GOTOOLCHAIN=local GOPROXY=off go test ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off go test -race ./...
```

Tests specify manual canonical bytes and a fixed digest, equivalent spelling
controls, each application field's identity binding, Unicode/control forms,
semantic set permutations/duplicates, exact schema/type failures, bounded
framing/depth/array/Ref limits, context mismatch, Status identity, copied results
and exact recovery sizing boundaries. These are parser and identity checks;
they do not qualify actual source normalization, selection docking, operation
replay, journal durability, one-Start behavior, readiness or a live launch tuple.
