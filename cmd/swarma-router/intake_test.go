package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// Explicit flags preserve caller inputs; ambiguous, unknown and malformed flags refuse.
func TestOptions(t *testing.T) {
	valid := []string{"recommend", "--task=t", "--member=m", "--state=s", "--policy=p", "--at=2026-11-17T11:00:00Z"}
	o, err := parseOptions(valid)
	if err != nil || len(o.missing) != 0 || o.format != "json" || o.task != "t" || o.at.IsZero() {
		t.Fatal(o, err)
	}
	for _, tail := range [][]string{{"--format=text"}, {"--format=json"}} {
		if _, err := parseOptions(append(append([]string{}, valid...), tail...)); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{nil, {"launch"}, append(append([]string{}, valid...), "extra"), append(append([]string{}, valid...), "--unknown=x"), append(append([]string{}, valid...), "--task=other"), append(append([]string{}, valid...), "--format=yaml"), {"recommend", "--at=tomorrow"}, {"recommend", "--at"}} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	o, err = parseOptions([]string{"recommend"})
	if err != nil || !reflect.DeepEqual(o.missing, []string{"task", "member", "state", "policy", "at"}) {
		t.Fatal(o, err)
	}
}

// Intake reads regular files through the exact bound and refuses oversized/special/link inputs without modifying them.
func TestOrdinaryBoundedInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	for _, size := range []int{0, 12, maxInputBytes, maxInputBytes + 1} {
		data := bytes.Repeat([]byte("x"), size)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		before, _ := os.Stat(path)
		got, err := readInput(path)
		if size <= maxInputBytes {
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal(size, err)
			}
		} else if err == nil {
			t.Fatal("oversized input accepted")
		}
		after, _ := os.Stat(path)
		current, _ := os.ReadFile(path)
		if !bytes.Equal(current, data) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("input modified")
		}
	}
	// Use a valid target so link rejection cannot be masked by the size guard.
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{dir, link, fifo, filepath.Join(dir, "absent")} {
		if _, err := readInput(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("unexpected filesystem effect", entries)
	}
}
