# Retained request binding

`workbench.RetainedRequest` is the immutable request-binding portion of the
local workbench operation core. It preserves the original validated Prepare,
canonical request digest, lookup tuple, operation reference and opaque durable
revision. It does not implement the complete operation record, storage, an
atomic compare-and-swap, replay responses, lifecycle transitions or any source
or execution adapter. Nothing in this API reserves, selects or starts work.

`RetainRequest(parsed, operationRef, durableRevision)` accepts only a validated
Prepare and nonempty bounded references. Status cannot create a binding. The
admitted store must supply its actual operation reference and confirmed durable
revision; constructing a value does not prove either is persisted.

`Encode()` produces a closed canonical JSON object followed by LF:

```text
{contract, key, request_digest, prepare_request, operation_ref, durable_revision}
```

The contract is `router-workbench-request-record/0.1.0`. `key` contains exactly
`caller`, `project`, `correlation_id`; `prepare_request` contains the complete
original canonical Prepare. Every field is required and non-null. This is a
separate request-binding envelope intended for a future complete operation
record; it is not an import or migration format for notice or launch journals.

`RestoreRetained(expectedKey, frame)` requires the exact expected lookup tuple,
valid bounded UTF-8 JSON+LF, the closed envelope and key schemas, a full typed
Prepare in normalized semantic order, and an independently recomputed digest
of every canonical request field. Unknown/duplicate decoded keys, malformed
Unicode, wrong tuple, changed request, incorrect digest and invalid metadata
return `store_invalid` with an empty value. That error is not reliable absence
and never authorizes record replacement, a new key or another selection.

Wire input and retained canonical content have different encoded limits.
`Parse` still admits at most 65536 bytes including LF. Canonicalization can
expand a valid short escape such as `\n` from two to six bytes. Consequently an
already-retained canonical request can legitimately exceed the wire input
limit. Restore validates it under the complete 262144-byte record limit and
the same typed predicates and recovery-capacity checks, without treating it as
a new wire request. The encoded request-binding envelope must also fit that
record limit. Later operation fields still need complete record-budget checks;
fitting this portion alone does not establish that the whole operation fits.

`Match(parsed)` accepts Prepare replay or Status only when tuple and original
digest match. A different digest under the same valid tuple returns
`idempotency_conflict`; an invalid value or wrong lookup tuple returns
`store_invalid`. A match grants no current readiness, source access, effect or
permission to repeat preparation. Current read/observation authority remains
an independent responsibility of the admitted operation embedding.

`AtRevision(expected)` compares exact opaque revision equality. It never orders
revision strings, changes a value or writes storage. A real update must compare
that expected revision inside the owning store's serialized transaction and
preserve uncertainty on a lost acknowledgement or CAS failure. This helper is
not a substitute for that transaction.

All public byte slices and typed request values are independent copies.
Representation consistency and SHA-256 do not authenticate stored data against
a hostile same-user writer or prove reference authenticity. Storage custody,
durability, request/source/proposal immutability across lifecycle updates,
same-operation observation, fresh ready responses and the possible-effect
single-Start boundary remain separate operation-core work.

Run `GOTOOLCHAIN=local GOPROXY=off go test ./workbench -run '^TestRetained' -count=1`
for exact envelope, full-request/key/digest conflict, copied-value, typed Status,
invalid record and control-expansion regression checks. These tests do not
exercise a disk store, actual CAS, source admission or a launch tuple.
