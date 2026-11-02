package workbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const RetainedRequestContract = "router-workbench-request-record/0.1.0"
const MaxRecordBytes = 262144

// RetainedRequest is the immutable request-binding portion of a future operation
// record. It is not a complete operation, receipt, durable reservation or permit
// to execute. Only the admitted store can establish persistence and atomic CAS.
type RetainedRequest struct {
	request                Request
	operationRef, revision string
}

// RetainRequest creates a value, not a journal write. The owning store supplies
// its actual operation reference and confirmed durable revision. Status cannot
// initialize a request binding or become a replacement Prepare.
func RetainRequest(r Request, operationRef, durableRevision string) (RetainedRequest, error) {
	if r.operation != "prepare_workbench" || r.prepare == nil || !ref(operationRef) || !ref(durableRevision) {
		return RetainedRequest{}, errors.New("store_invalid")
	}
	return RetainedRequest{request: r, operationRef: operationRef, revision: durableRevision}, nil
}

func (r RetainedRequest) Key() OperationKey               { return r.request.Key() }
func (r RetainedRequest) Digest() string                  { return r.request.Digest() }
func (r RetainedRequest) OperationRef() string            { return r.operationRef }
func (r RetainedRequest) DurableRevision() string         { return r.revision }
func (r RetainedRequest) CanonicalPrepare() []byte        { return r.request.CanonicalPrepare() }
func (r RetainedRequest) Prepare() (PrepareRequest, bool) { return r.request.Prepare() }

// Match validates a proposed Prepare replay or Status lookup against the same
// retained tuple and digest. Success never means current readiness or permission
// to rerun source acquisition, selection or preparation.
func (r RetainedRequest) Match(in Request) error {
	if r.request.prepare == nil || !ref(r.operationRef) || !ref(r.revision) || in.operation == "" {
		return errors.New("store_invalid")
	}
	if r.Key() != in.Key() {
		return errors.New("store_invalid")
	}
	if r.Digest() != in.Digest() {
		return errors.New("idempotency_conflict")
	}
	return nil
}

// AtRevision compares an opaque durable revision without ordering or mutation.
// A passing comparison is not an atomic CAS: the store must compare the same
// expected revision inside its own serialized transaction when writing updates.
func (r RetainedRequest) AtRevision(expected string) bool {
	return r.request.prepare != nil && ref(expected) && r.revision == expected
}

// Encode returns the closed request-binding envelope as canonical JSON+LF.
// This format is distinct from notice/launch journals and never imports them.
// A future operation record adds its own lifecycle and durability validation;
// this envelope alone cannot represent a complete operation or ready receipt.
func (r RetainedRequest) Encode() ([]byte, error) {
	if r.request.prepare == nil || !ref(r.operationRef) || !ref(r.revision) {
		return nil, errors.New("store_invalid")
	}
	var request any
	if err := json.Unmarshal(r.request.canonical, &request); err != nil {
		return nil, errors.New("store_invalid")
	}
	k := r.Key()
	m := map[string]any{
		"contract":       RetainedRequestContract,
		"key":            map[string]any{"caller": k.Caller, "project": k.Project, "correlation_id": k.CorrelationID},
		"request_digest": r.Digest(), "prepare_request": request,
		"operation_ref": r.operationRef, "durable_revision": r.revision,
	}
	b := append(canonical(m), '\n')
	if len(b) > MaxRecordBytes {
		return nil, errors.New("store_invalid")
	}
	return b, nil
}

// RestoreRetained validates the complete closed envelope, the expected lookup
// tuple, all original request fields and the recomputed full canonical digest.
// It does not authenticate stored bytes or infer reliable absence on an error.
// Callers must preserve a malformed/unreadable operation rather than recreate it.
func RestoreRetained(expected OperationKey, frame []byte) (RetainedRequest, error) {
	fail := func() (RetainedRequest, error) { return RetainedRequest{}, errors.New("store_invalid") }
	if !ref(expected.Caller) || !ref(expected.Project) || !ref(expected.CorrelationID) || len(frame) < 3 || len(frame) > MaxRecordBytes || frame[len(frame)-1] != '\n' || frame[len(frame)-2] != '}' || !utf8.Valid(frame) || !scalarEscapes(frame) {
		return fail()
	}
	d := json.NewDecoder(bytes.NewReader(frame[:len(frame)-1]))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return fail()
	}
	if _, err := d.Token(); err != io.EOF {
		return fail()
	}
	m, ok := v.(map[string]any)
	if !ok || !keys(m, "contract", "key", "request_digest", "prepare_request", "operation_ref", "durable_revision") || m["contract"] != RetainedRequestContract || !digest(m["request_digest"]) || !ref(m["operation_ref"]) || !ref(m["durable_revision"]) {
		return fail()
	}
	k, ok := m["key"].(map[string]any)
	if !ok || !keys(k, "caller", "project", "correlation_id") || k["caller"] != expected.Caller || k["project"] != expected.Project || k["correlation_id"] != expected.CorrelationID {
		return fail()
	}
	// This is already-retained canonical content, not a new wire frame. A
	// legitimate 64KiB input can grow beyond 64KiB when short control escapes
	// become six-byte canonical escapes. Apply the same typed predicates and
	// semantic order, under the distinct complete-record budget instead.
	p, ok := m["prepare_request"].(map[string]any)
	if !ok || p["contract"] != Contract || p["operation"] != "prepare_workbench" || p["caller"] != expected.Caller || p["project"] != expected.Project || p["correlation_id"] != expected.CorrelationID {
		return fail()
	}
	b := canonical(p)
	if !validPrepare(p) || !bytes.Equal(canonical(p), b) {
		return fail()
	}
	h := sha256.Sum256(b)
	r := Request{key: expected, operation: "prepare_workbench", digest: hex.EncodeToString(h[:]), canonical: b, prepare: new(PrepareRequest)}
	if r.digest != m["request_digest"] || json.Unmarshal(b, r.prepare) != nil {
		return fail()
	}
	r.recovery = recoveryTemplate(r)
	if len(r.recovery) > MaxRecoveryBytes {
		return fail()
	}
	retained, err := RetainRequest(r, m["operation_ref"].(string), m["durable_revision"].(string))
	if err != nil {
		return fail()
	}
	if _, err := retained.Encode(); err != nil {
		return fail()
	}
	return retained, nil
}
