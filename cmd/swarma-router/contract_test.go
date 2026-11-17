package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type wireCase struct {
	Name     string                     `json:"name"`
	Changes  map[string]json.RawMessage `json:"changes"`
	Remove   []string                   `json:"remove"`
	Expected json.RawMessage            `json:"expected"`
}

// Accepted wire examples exercise the real CLI and engine together; a dependency fixture cannot satisfy this test.
func TestContractExamples(t *testing.T) {
	b, err := os.ReadFile("testdata/recommend-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		At     string                     `json:"at"`
		Inputs map[string]json.RawMessage `json:"inputs"`
		Cases  []wireCase                 `json:"cases"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			docs := map[string]map[string]any{}
			for name, raw := range fixture.Inputs {
				var doc map[string]any
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				docs[strings.TrimSuffix(name, ".json")] = doc
			}
			for path, value := range tc.Changes {
				var v any
				if err := json.Unmarshal(value, &v); err != nil {
					t.Fatal(err)
				}
				changeFixture(t, docs, path, v, false)
			}
			for _, path := range tc.Remove {
				changeFixture(t, docs, path, nil, true)
			}
			dir := t.TempDir()
			args := []string{"recommend"}
			for _, name := range []string{"task", "member", "state", "policy"} {
				p := filepath.Join(dir, name+".json")
				raw, err := json.Marshal(docs[name])
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, raw, 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--"+name, p)
			}
			args = append(args, "--at", fixture.At)
			before := snapshotInputs(t, dir)
			var out bytes.Buffer
			exit := run(args, &out)
			var want, got map[string]any
			if json.Unmarshal(tc.Expected, &want) != nil || json.Unmarshal(out.Bytes(), &got) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("expected %s; got exit%d %s", tc.Expected, exit, out.String())
			}
			expectedExit := map[string]int{"recommended": 0, "refused": 1, "insufficient_evidence": 2, "defer": 3}[want["status"].(string)]
			if exit != expectedExit {
				t.Fatal("wrong exit", exit, expectedExit)
			}
			if !reflect.DeepEqual(before, snapshotInputs(t, dir)) {
				t.Fatal("input files changed or output files created")
			}
			out.Reset()
			if textExit := run(append(args, "--format=text"), &out); textExit != exit || strings.Count(out.String(), "\n") != 1 {
				t.Fatal(textExit, out.String())
			}
			if want["status"] != "recommended" && (strings.Contains(out.String(), "action=") || strings.Contains(out.String(), "runtime=")) {
				t.Fatal("error/defer has a target", out.String())
			}
			if !reflect.DeepEqual(before, snapshotInputs(t, dir)) {
				t.Fatal("text call changed inputs")
			}
		})
	}
}

// Mutation paths here belong only to the neutral accepted test fixture.
func changeFixture(t *testing.T, docs map[string]map[string]any, path string, value any, remove bool) {
	t.Helper()
	parts := strings.Split(path, ".")
	doc := docs[parts[0]]
	if doc == nil {
		t.Fatal("unknown fixture document", path)
	}
	for _, part := range parts[1 : len(parts)-1] {
		if i := strings.IndexByte(part, '['); i >= 0 {
			key, index := part[:i], strings.TrimSuffix(part[i+1:], "]")
			items := doc[key].([]any)
			var selected map[string]any
			if n, err := strconv.Atoi(index); err == nil {
				selected = items[n].(map[string]any)
			} else {
				for _, item := range items {
					m := item.(map[string]any)
					if m["scope"] == index {
						selected = m
						break
					}
				}
			}
			if selected == nil {
				t.Fatal("unknown fixture index", path)
			}
			doc = selected
		} else {
			doc = doc[part].(map[string]any)
		}
	}
	key := parts[len(parts)-1]
	if remove {
		delete(doc, key)
	} else {
		doc[key] = value
	}
}

type fileWitness struct {
	Mode     os.FileMode
	Size     int64
	Modified int64
	Content  string
}

func snapshotInputs(t *testing.T, dir string) map[string]fileWitness {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fileWitness{}
	for _, entry := range entries {
		p := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		witness := fileWitness{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			witness.Content = string(raw)
		}
		out[entry.Name()] = witness
	}
	return out
}
