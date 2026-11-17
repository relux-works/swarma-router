package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"relux.works/swarma-router/recommend"
)

func cliInputs(t *testing.T) ([]string, []string) {
	t.Helper()
	dir := t.TempDir()
	args := []string{"recommend"}
	var paths []string
	for _, name := range []string{"task", "member", "state", "policy"} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(" {\n  \""+name+"\": true\n } \n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--"+name, path)
		paths = append(paths, path)
	}
	return append(args, "--at", "2026-11-17T11:00:00Z"), paths
}

func sampleResult() recommend.Result {
	reset := false
	return recommend.Result{Contract: recommend.Contract, Status: "recommended", TaskID: "EXAMPLE-001", RuleID: "R06", Reason: "Current configuration remains preferred and within policy.", Action: "keep", Selected: "standard-high", Runtime: "codex", Model: "model-standard", Effort: "high", ContextReset: &reset}
}

// The CLI forwards original bytes and explicit time to the agreed API with the default classifier; it has no input writes.
func TestCLIPassthrough(t *testing.T) {
	args, paths := cliInputs(t)
	var calls int
	var out bytes.Buffer
	want := sampleResult()
	before := make([][]byte, len(paths))
	infos := make([]os.FileInfo, len(paths))
	for i, p := range paths {
		before[i], _ = os.ReadFile(p)
		infos[i], _ = os.Stat(p)
	}
	decide := func(raw recommend.RawInputs, at time.Time, c recommend.Classifier) recommend.Result {
		calls++
		for i, b := range [][]byte{raw.Task, raw.Member, raw.State, raw.Policy} {
			if !bytes.Equal(b, before[i]) {
				t.Fatal("raw input changed", i)
			}
		}
		if at.Format(time.RFC3339) != "2026-11-17T11:00:00Z" || c != nil {
			t.Fatal("implicit time/classifier", at, c)
		}
		return want
	}
	if exit := runWithRecommender(args, &out, decide); exit != 0 || calls != 1 {
		t.Fatal(exit, calls, out.String())
	}
	var got recommend.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(err, got)
	}
	for i, p := range paths {
		b, _ := os.ReadFile(p)
		info, _ := os.Stat(p)
		if !bytes.Equal(b, before[i]) || info.Mode() != infos[i].Mode() || !info.ModTime().Equal(infos[i].ModTime()) {
			t.Fatal("input modified")
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(paths[0]))
	if len(entries) != 4 {
		t.Fatal("unexpected file created")
	}
}

// Missing facts and invalid boundaries cannot invoke the engine or return a target; path strings are never disclosed.
func TestCLIRefusals(t *testing.T) {
	args, paths := cliInputs(t)
	for _, tc := range []struct {
		name         string
		prepare      func() []string
		status, code string
		exit         int
	}{
		{"missing-flags", func() []string { return []string{"recommend"} }, "insufficient_evidence", "missing_input", 2},
		{"invalid-time", func() []string { return append(append([]string{}, args[:len(args)-1]...), "tomorrow") }, "refused", "invalid_cli", 1},
		{"unknown-flag", func() []string { return append(append([]string{}, args...), "--launch=true") }, "refused", "invalid_cli", 1},
		{"missing-file", func() []string { a := append([]string{}, args...); a[2] = paths[0] + "-missing"; return a }, "insufficient_evidence", "missing_input", 2},
		{"directory", func() []string { a := append([]string{}, args...); a[2] = filepath.Dir(paths[0]); return a }, "refused", "invalid_input_file", 1},
		{"oversize", func() []string {
			a := append([]string{}, args...)
			p := paths[0] + "-large"
			if err := os.WriteFile(p, bytes.Repeat([]byte("x"), maxInputBytes+1), 0600); err != nil {
				t.Fatal(err)
			}
			a[2] = p
			return a
		}, "refused", "invalid_input_file", 1},
		{"symlink", func() []string {
			a := append([]string{}, args...)
			p := paths[0] + "-link"
			if err := os.Symlink(paths[0], p); err != nil {
				t.Fatal(err)
			}
			a[2] = p
			return a
		}, "refused", "invalid_input_file", 1},
		{"fifo", func() []string {
			a := append([]string{}, args...)
			p := paths[0] + "-fifo"
			if err := unix.Mkfifo(p, 0600); err != nil {
				t.Fatal(err)
			}
			a[2] = p
			return a
		}, "refused", "invalid_input_file", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare once per case; use the same boundary for both output formats.
			a := tc.prepare()
			before := snapshotInputs(t, filepath.Dir(paths[0]))
			for _, f := range []string{"json", "text"} {
				var out bytes.Buffer
				calls := 0
				exit := runWithRecommender(append(append([]string{}, a...), "--format="+f), &out, func(recommend.RawInputs, time.Time, recommend.Classifier) recommend.Result {
					calls++
					return sampleResult()
				})
				if !reflect.DeepEqual(before, snapshotInputs(t, filepath.Dir(paths[0]))) {
					t.Fatal("failed intake changed files")
				}
				if exit != tc.exit || calls != 0 {
					t.Fatal(exit, calls, out.String())
				}
				if strings.Contains(out.String(), paths[0]) || strings.Contains(out.String(), "runtime") || strings.Contains(out.String(), "context_reset") {
					t.Fatal("unexpected target/path", out.String())
				}
				if f == "json" {
					var r recommend.Result
					if json.Unmarshal(out.Bytes(), &r) != nil || r.Status != tc.status || r.Code != tc.code || r.ExecutionAuthority {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), "status="+`"`+tc.status+`"`) {
					t.Fatal(out.String())
				}
			}
		})
	}
}

// Both formats preserve each API status and exit; nonrecommendations carry no target, and text cannot inject extra lines.
func TestCLIOutputStatuses(t *testing.T) {
	args, _ := cliInputs(t)
	for _, tc := range []struct {
		status string
		exit   int
	}{{"recommended", 0}, {"refused", 1}, {"insufficient_evidence", 2}, {"defer", 3}} {
		for _, format := range []string{"json", "text"} {
			r := sampleResult()
			if tc.status != "recommended" {
				r = recommend.Result{Contract: recommend.Contract, Status: tc.status, RuleID: "R01", Reason: "Reason\nwith controls", Code: "example", Missing: []string{"state.context_used_bps"}}
			}
			var out bytes.Buffer
			exit := runWithRecommender(append(append([]string{}, args...), "--format="+format), &out, func(recommend.RawInputs, time.Time, recommend.Classifier) recommend.Result { return r })
			if exit != tc.exit || strings.Count(out.String(), "\n") != 1 {
				t.Fatal(exit, out.String())
			}
			if format == "json" {
				var got recommend.Result
				if json.Unmarshal(out.Bytes(), &got) != nil || !reflect.DeepEqual(got, r) {
					t.Fatal(out.String())
				}
			} else if tc.status != "recommended" && strings.Contains(out.String(), "action=") {
				t.Fatal(out.String())
			}
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }

// Failed output is a nonzero failure in either format, never successful recommendation delivery.
func TestCLIOutputFailure(t *testing.T) {
	for _, f := range []string{"json", "text"} {
		if code := writeResult(io.Writer(brokenWriter{}), f, sampleResult()); code != 1 {
			t.Fatal(code)
		}
	}
}

// Effort-only switches retain the engine's reset=true in both formats; CLI cannot turn a switch into keep.
func TestCLISwitchOutput(t *testing.T) {
	args, _ := cliInputs(t)
	r := sampleResult()
	r.Action = "switch"
	r.Effort = "low"
	reset := true
	r.ContextReset = &reset
	for _, format := range []string{"json", "text"} {
		var out bytes.Buffer
		if exit := runWithRecommender(append(append([]string{}, args...), "--format="+format), &out, func(recommend.RawInputs, time.Time, recommend.Classifier) recommend.Result { return r }); exit != 0 {
			t.Fatal(exit, out.String())
		}
		if format == "json" {
			var got recommend.Result
			if json.Unmarshal(out.Bytes(), &got) != nil || !reflect.DeepEqual(got, r) {
				t.Fatal(out.String())
			}
		} else if !strings.Contains(out.String(), `action="switch"`) || !strings.Contains(out.String(), "context_reset=true") {
			t.Fatal(out.String())
		}
	}
}
