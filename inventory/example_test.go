package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"relux.works/swarma-router/inventory"
)

// syntheticSource is only this example's synchronous, in-memory DATA producer.
type syntheticSource struct {
	frame         []byte
	completionErr error
}

func (s syntheticSource) Feed(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := w.Write(s.frame); err != nil {
		return err
	}
	return s.completionErr
}

// Read admits a complete frame with explicit unknowns, but publishes no partial
// projection if that same producer fails after writing. A usable DATA result
// establishes neither source authenticity, current readiness nor launch authority.
func ExampleRead() {
	binding := inventory.Binding{
		SourceVersions: []string{"example-source-1"}, OwnerBindingRef: "example-receipt",
		Member: "example-member", Project: "example-project", Workbench: "example-workbench",
		Execution: "example-execution", RunnerInstance: "example-runner",
	}
	facts := map[string]any{}
	for _, name := range []string{
		"controller_epoch", "native_thread", "selected_adapter", "observed_release",
		"configured_vendor", "configured_model", "native_vendor", "native_model",
		"requested_effort", "effective_effort", "profile", "profile_lock", "placement",
		"native_activity", "current_turn", "input_state", "security_stopped",
		"stop_requested", "fence", "messaging_binding", "readiness",
	} {
		facts[name] = map[string]any{"known": false, "reason": "not-exposed"}
	}
	facts["configured_model"] = map[string]any{
		"known": true, "value": "example-model", "provenance": "configured",
		"source_observed_at": nil, "source_expires_at": nil,
	}
	frame, err := json.Marshal(map[string]any{
		"contract": inventory.Contract, "source_version": binding.SourceVersions[0],
		"trust_mode": "local_single_user_unverified", "owner_binding_ref": binding.OwnerBindingRef,
		"member": binding.Member, "project": binding.Project, "workbench": binding.Workbench,
		"execution": binding.Execution, "runner_instance": binding.RunnerInstance,
		"collected_at": "2026-01-02T12:00:00Z", "coverage": "owned_execution_partial", "facts": facts,
	})
	if err != nil {
		panic(err)
	}
	frame = append(frame, '\n')
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	good := inventory.Read(ctx, syntheticSource{frame: frame}, binding)
	fmt.Println(good.Class, good.Projection.Coverage)
	model := good.Projection.Facts["configured_model"]
	fmt.Printf("configured model: %v, source time unknown: %t\n", model.Value, model.SourceObservedAt == nil)
	fmt.Println("readiness known:", good.Projection.Facts["readiness"].Known)

	failed := inventory.Read(ctx, syntheticSource{frame: frame, completionErr: errors.New("synthetic completion failure")}, binding)
	fmt.Println(failed.Class, failed.Reason, "projection absent:", failed.Projection == nil)
	// Output:
	// usable owned_execution_partial
	// configured model: example-model, source time unknown: true
	// readiness known: false
	// unavailable source-unavailable projection absent: true
}
