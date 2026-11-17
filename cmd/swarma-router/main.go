package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"relux.works/swarma-router/recommend"
)

type recommender func(recommend.RawInputs, time.Time, recommend.Classifier) recommend.Result

func run(args []string, out io.Writer) int {
	return runWithRecommender(args, out, recommend.Recommend)
}

func runWithRecommender(args []string, out io.Writer, decide recommender) int {
	o, err := parseOptions(args)
	refused := func(code, reason string) int {
		return writeResult(out, o.format, recommend.Result{Contract: recommend.Contract, Status: "refused", RuleID: "R00", Reason: reason, Code: code})
	}
	if err != nil {
		return refused("invalid_cli", "Invalid command, flags, format or decision time.")
	}
	if len(o.missing) > 0 {
		return writeResult(out, o.format, recommend.Result{Contract: recommend.Contract, Status: "insufficient_evidence", RuleID: "R01", Reason: "Required CLI input is missing.", Code: "missing_input", Missing: o.missing})
	}
	var raw recommend.RawInputs
	inputs := []struct {
		name, path string
		dst        *[]byte
	}{
		{"task", o.task, &raw.Task}, {"member", o.member, &raw.Member}, {"state", o.state, &raw.State}, {"policy", o.policy, &raw.Policy},
	}
	var missing []string
	for _, input := range inputs {
		b, readErr := readInput(input.path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, os.ErrPermission) {
				missing = append(missing, input.name)
				continue
			}
			return refused("invalid_input_file", "Invalid "+input.name+" input file.")
		}
		*input.dst = b
	}
	if len(missing) > 0 {
		return writeResult(out, o.format, recommend.Result{Contract: recommend.Contract, Status: "insufficient_evidence", RuleID: "R01", Reason: "Required input file is unavailable.", Code: "missing_input", Missing: missing})
	}
	return writeResult(out, o.format, decide(raw, o.at, nil))
}

func writeResult(out io.Writer, format string, r recommend.Result) int {
	var err error
	if format == "text" {
		fields := []string{"status=" + strconv.Quote(r.Status), "rule_id=" + strconv.Quote(r.RuleID), "reason=" + strconv.Quote(r.Reason), fmt.Sprintf("execution_authority=%t", r.ExecutionAuthority)}
		if r.TaskID != "" {
			fields = append(fields, "task_id="+strconv.Quote(r.TaskID))
		}
		if r.Status == "recommended" {
			fields = append(fields, "action="+strconv.Quote(r.Action), "selected="+strconv.Quote(r.Selected), "runtime="+strconv.Quote(r.Runtime), "model="+strconv.Quote(r.Model), "effort="+strconv.Quote(r.Effort))
			if r.ContextReset != nil {
				fields = append(fields, fmt.Sprintf("context_reset=%t", *r.ContextReset))
			}
		}
		if r.Code != "" {
			fields = append(fields, "code="+strconv.Quote(r.Code))
		}
		if len(r.Missing) > 0 {
			b, _ := json.Marshal(r.Missing)
			fields = append(fields, "missing="+string(b))
		}
		_, err = fmt.Fprintln(out, strings.Join(fields, " "))
	} else {
		err = json.NewEncoder(out).Encode(r)
	}
	if err != nil {
		return 1
	}
	return r.ExitCode()
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
