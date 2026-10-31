package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"relux.works/swarma-router/handoff"
)

func run(ctx context.Context, args []string, out io.Writer) int {
	return runWithEngine(ctx, args, out, handoff.Engine{Adapter: handoff.CLIAdapter{Runner: handoff.ExecRunner{}}})
}

func runWithEngine(ctx context.Context, args []string, out io.Writer, engine handoff.Engine) int {
	result := handoff.Result{Contract: handoff.Contract, Status: "refused", Reason: "invalid_cli"}
	write := func() int {
		if json.NewEncoder(out).Encode(result) != nil {
			return 3
		}
		return result.ExitCode()
	}
	flags := flag.NewFlagSet("queued-task-handoff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	request := flags.String("request", "", "explicit request JSON file")
	policy := flags.String("policy", "", "explicit private local policy JSON file")
	journal := flags.String("journal", "", "durable private journal directory")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *request == "" || *policy == "" || *journal == "" {
		return write()
	}
	b, err := handoff.ReadFile(*request, false)
	var r handoff.Request
	if err != nil || handoff.Decode(b, &r) != nil {
		result.Reason = "invalid_request_file"
		return write()
	}
	b, err = handoff.ReadFile(*policy, true)
	var p handoff.Policy
	if err != nil || handoff.Decode(b, &p) != nil {
		result.Reason = "invalid_policy_file"
		return write()
	}
	result = engine.Execute(ctx, r, p, *journal)
	return write()
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout))
}
