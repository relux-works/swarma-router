package workbench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fileFixture(t *testing.T) (FileRepository, Request, context.Context) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return FileRepository{filepath.Join(parent, "operations")}, mustParse(t, []byte(goldenPrepare+"\n"), local), ctx
}

func openFileFixture(t *testing.T, repo FileRepository, req Request, ctx context.Context) *fileOperationJournal {
	t.Helper()
	j, err := repo.Open(ctx, req.Key())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j.(*fileOperationJournal)
}

func fileRecord(t *testing.T, req Request, revision, phase string) []byte {
	t.Helper()
	binding, err := RetainRequest(req, "original-operation", revision)
	if err != nil {
		t.Fatal(err)
	}
	r := operationRecord{Contract: OperationContract, Phase: phase}
	if phase != "resolving" {
		f, _, _ := newOperationFixture(t, "reuse")
		r.Sources, r.Proposal, r.Selected, r.Action = f.plan.Sources, f.plan.Proposal, f.plan.Selected, "reuse"
	}
	_, b, err := encodeOperation(r, binding)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileCommit(t *testing.T, j Journal, req Request, ctx context.Context, expected, phase string) ([]byte, string) {
	t.Helper()
	var written []byte
	var revision string
	calls := 0
	err := j.Commit(ctx, expected, func(next string) ([]byte, error) {
		calls++
		revision = next
		written = fileRecord(t, req, next, phase)
		return written, nil
	})
	if err != nil || calls != 1 || !ref(revision) || revision == expected {
		t.Fatal("commit protocol", err, calls, revision)
	}
	return written, revision
}

// Real private files round-trip exact bytes and revisions across reopen. CAS
// conflicts run no builder, preserve bytes, and do not block a valid next write.
func TestFileRepositoryCASAndReopen(t *testing.T) {
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	if raw, err := j.Read(ctx); raw != nil || err != nil {
		t.Fatal("reliable absence", err)
	}
	original, rev := fileCommit(t, j, req, ctx, "", "resolving")
	for _, stale := range []string{"", "other-revision"} {
		calls := 0
		if err := j.Commit(ctx, stale, func(next string) ([]byte, error) { calls++; return fileRecord(t, req, next, "selected"), nil }); err == nil || err.Error() != "revision conflict" || calls != 0 {
			t.Fatal("stale CAS admitted", err, calls)
		}
	}
	raw, err := j.Read(ctx)
	if err != nil || !bytes.Equal(original, raw) {
		t.Fatal("CAS changed bytes", err)
	}
	raw[0] = 'x'
	if got, err := j.Read(ctx); err != nil || !bytes.Equal(original, got) {
		t.Fatal("read alias", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = openFileFixture(t, repo, req, ctx)
	if got, err := j.Read(ctx); err != nil || !bytes.Equal(original, got) {
		t.Fatal("reopen lost record", err)
	}
	updated, _ := fileCommit(t, j, req, ctx, rev, "selected")
	if got, err := os.ReadFile(j.path()); err != nil || !bytes.Equal(updated, got) {
		t.Fatal("acknowledged bytes differ", err)
	}
	if got, err := filepath.Glob(filepath.Join(repo.Directory, ".workbench-v2-*")); err != nil || len(got) != 0 {
		t.Fatal("temporary write leaked", got, err)
	}
}

// Different complete keys have distinct names; transplanting a valid record to
// another key refuses, rather than discovering or resetting that operation.
func TestFileRepositoryKeyIsolation(t *testing.T) {
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	original, _ := fileCommit(t, j, req, ctx, "", "resolving")
	originalPath := j.path()
	j.Close()
	for _, field := range []string{"caller", "project", "correlation_id"} {
		m := base(t)
		m[field] = "other"
		x := mustParse(t, frame(t, m), LocalContext{m["caller"].(string), m["project"].(string)})
		xj := openFileFixture(t, repo, x, ctx)
		if xj.path() == originalPath {
			t.Fatal("key collision")
		}
		if raw, err := xj.Read(ctx); err != nil || raw != nil {
			t.Fatal("key lookup discovered another record", err)
		}
		if err := os.WriteFile(xj.path(), original, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := xj.Read(ctx); err == nil {
			t.Fatal("transplanted key accepted")
		}
		calls := 0
		if err := xj.Commit(ctx, "", func(string) ([]byte, error) { calls++; return nil, nil }); err == nil || calls != 0 {
			t.Fatal("corruption reset", err, calls)
		}
		if after, _ := os.ReadFile(xj.path()); !bytes.Equal(after, original) {
			t.Fatal("transplant overwritten")
		}
		xj.Close()
	}
}

// Directory custody, file custody and aggregate bounds have valid neighbors.
// Rejected files are preserved; no links are followed and no old-format files
// are discovered, read, migrated or removed by the workbench store.
func TestFileRepositoryCustodyAndBounds(t *testing.T) {
	for _, mode := range []string{"valid", "exact-limit", "oversize", "corrupt", "public-file", "symlink", "hardlink", "fifo", "directory"} {
		t.Run(mode, func(t *testing.T) {
			repo, req, ctx := fileFixture(t)
			j := openFileFixture(t, repo, req, ctx)
			original := fileRecord(t, req, "revision", "resolving")
			switch mode {
			case "exact-limit":
				original = append(bytes.Repeat([]byte(" "), MaxRecordBytes-len(original)), original...)
			case "oversize":
				original = append(bytes.Repeat([]byte(" "), MaxRecordBytes-len(original)+1), original...)
			case "corrupt":
				original = []byte("{\"contract\":\"corrupt\"}\n")
			}
			target := filepath.Join(filepath.Dir(repo.Directory), "external")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "symlink":
				err = os.Symlink(target, j.path())
			case "hardlink":
				err = os.Link(target, j.path())
			case "fifo":
				err = syscall.Mkfifo(j.path(), 0600)
			case "directory":
				err = os.Mkdir(j.path(), 0700)
			default:
				err = os.WriteFile(j.path(), original, 0600)
				if mode == "public-file" && err == nil {
					err = os.Chmod(j.path(), 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(repo.Directory, "launch-legacy.json")
			if err := os.WriteFile(legacy, []byte("unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := j.Read(ctx)
			valid := mode == "valid" || mode == "exact-limit"
			if valid && (err != nil || !bytes.Equal(got, original)) || !valid && (err == nil || got != nil) {
				t.Fatal("custody/bound verdict", mode, err)
			}
			if !valid {
				calls := 0
				if e := j.Commit(ctx, "", func(string) ([]byte, error) { calls++; return nil, nil }); e == nil || calls != 0 {
					t.Fatal("invalid file overwritten", e, calls)
				}
			}
			if external, _ := os.ReadFile(target); !bytes.Equal(external, original) {
				t.Fatal("external target changed")
			}
			if old, _ := os.ReadFile(legacy); string(old) != "unrelated" {
				t.Fatal("legacy record touched")
			}
		})
	}
	for _, mode := range []string{"public-dir", "symlink-dir", "symlink-parent", "missing-parent", "empty"} {
		t.Run(mode, func(t *testing.T) {
			repo, req, ctx := fileFixture(t)
			parent := filepath.Dir(repo.Directory)
			switch mode {
			case "public-dir":
				if err := os.Mkdir(repo.Directory, 0755); err != nil {
					t.Fatal(err)
				}
				// Establish the forbidden mode even when the process umask removes it.
				if err := os.Chmod(repo.Directory, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink-dir", "symlink-parent":
				if err := os.Symlink(parent, filepath.Join(parent, "link")); err != nil {
					t.Fatal(err)
				}
				repo.Directory = filepath.Join(parent, "link")
				if mode == "symlink-parent" {
					repo.Directory = filepath.Join(repo.Directory, "new")
				}
			case "missing-parent":
				repo.Directory = filepath.Join(repo.Directory, "nested")
			case "empty":
				repo.Directory = ""
			}
			if j, err := repo.Open(ctx, req.Key()); err == nil {
				j.Close()
				t.Fatal("directory custody admitted", mode)
			}
		})
	}
}

// Rejected builders run at most once and leave the prior record intact. A
// correctly bound next record still commits after these pre-write refusals.
func TestFileRepositoryBuilderRefusals(t *testing.T) {
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	original, rev := fileCommit(t, j, req, ctx, "", "resolving")
	for _, mode := range []string{"error", "corrupt", "wrong-revision", "wrong-key", "wrong-digest", "wrong-operation", "regression", "oversize", "nil-builder"} {
		calls := 0
		build := func(next string) ([]byte, error) {
			calls++
			switch mode {
			case "error":
				return nil, errors.New("builder refusal")
			case "corrupt":
				return []byte("{}\n"), nil
			case "wrong-revision":
				return fileRecord(t, req, "another", "selected"), nil
			case "regression":
				return fileRecord(t, req, next, "resolving"), nil
			case "oversize":
				return bytes.Repeat([]byte(" "), MaxRecordBytes+1), nil
			case "wrong-key":
				m := base(t)
				m["correlation_id"] = "different"
				return fileRecord(t, mustParse(t, frame(t, m), local), next, "selected"), nil
			case "wrong-digest":
				m := base(t)
				m["constraints"].(map[string]any)["objective"] = "different"
				return fileRecord(t, mustParse(t, frame(t, m), local), next, "selected"), nil
			case "wrong-operation":
				b := fileRecord(t, req, next, "selected")
				return bytes.ReplaceAll(b, []byte("original-operation"), []byte("different-operation")), nil
			}
			return nil, nil
		}
		if mode == "nil-builder" {
			build = nil
		}
		if err := j.Commit(ctx, rev, build); err == nil || calls > 1 || mode != "nil-builder" && calls != 1 {
			t.Fatal("builder refusal", mode, err, calls)
		}
		if got, err := j.Read(ctx); err != nil || !bytes.Equal(got, original) {
			t.Fatal("builder changed old bytes", mode, err)
		}
	}
	fileCommit(t, j, req, ctx, rev, "selected")
}

// Every write-stage error refuses acknowledgement, releases the lock on Close,
// and preserves either the entire old or entire new record. Reopen confirms
// an unacknowledged rename; the failed generation never retries its builder.
// These injected failures are not a claim of power-loss/filesystem qualification.
func TestFileRepositoryWriteFaults(t *testing.T) {
	for _, stage := range []string{"create-temp", "write", "sync-file", "close-file", "rename", "sync-directory", "acknowledge"} {
		t.Run(stage, func(t *testing.T) {
			repo, req, ctx := fileFixture(t)
			j := openFileFixture(t, repo, req, ctx)
			old, rev := fileCommit(t, j, req, ctx, "", "resolving")
			var built []byte
			calls, plants := 0, 0
			j.step = func(s string) error {
				if s == stage {
					plants++
					return errors.New("injected " + stage)
				}
				return nil
			}
			err := j.Commit(ctx, rev, func(next string) ([]byte, error) {
				calls++
				built = fileRecord(t, req, next, "selected")
				return built, nil
			})
			if err == nil || err.Error() != "injected "+stage || calls != 1 || plants != 1 {
				t.Fatal("fault not exercised", err, calls, plants)
			}
			if stage != "create-temp" {
				if err := j.Commit(ctx, rev, func(string) ([]byte, error) { t.Fatal("uncertain write retried"); return nil, nil }); err == nil || err.Error() != "journal requires reopen" {
					t.Fatal("poisoned custody reused", err)
				}
			}
			j.Close()
			j = openFileFixture(t, repo, req, ctx)
			want := old
			if stage == "sync-directory" || stage == "acknowledge" {
				want = built
			}
			if got, err := j.Read(ctx); err != nil || !bytes.Equal(got, want) {
				t.Fatal("torn/lost record after reopen", err)
			}
			if temps, _ := filepath.Glob(filepath.Join(repo.Directory, ".workbench-v2-*")); len(temps) != 0 {
				t.Fatal("temp leaked", temps)
			}
		})
	}
}

// Read durability failures remain errors with no builder/write, never absence.
// Cancellation and a closed journal likewise reject without changing records.
func TestFileRepositoryReadFailuresAndContext(t *testing.T) {
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	original, rev := fileCommit(t, j, req, ctx, "", "resolving")
	for _, stage := range []string{"read-sync-file", "read-sync-directory"} {
		plants := 0
		j.step = func(s string) error {
			if s == stage {
				plants++
				return errors.New("read durability")
			}
			return nil
		}
		if got, err := j.Read(ctx); err == nil || got != nil {
			t.Fatal("read failure became data/absence", err)
		}
		if err := j.Commit(ctx, rev, func(string) ([]byte, error) { t.Fatal("builder after failed read"); return nil, nil }); err == nil || plants != 2 {
			t.Fatal("read fault not applied", err, plants)
		}
	}
	j.step = nil
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, c := range []context.Context{nil, canceled} {
		if other, err := repo.Open(c, req.Key()); err == nil {
			other.Close()
			t.Fatal("invalid context open")
		}
		if raw, err := j.Read(c); err == nil || raw != nil {
			t.Fatal("invalid context read")
		}
		if err := j.Commit(c, rev, func(string) ([]byte, error) { t.Fatal("invalid context builder"); return nil, nil }); err == nil {
			t.Fatal("invalid context commit")
		}
	}
	during, cancelDuring := context.WithCancel(ctx)
	if err := j.Commit(during, rev, func(next string) ([]byte, error) { cancelDuring(); return fileRecord(t, req, next, "selected"), nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("builder cancellation ignored", err)
	}
	if raw, err := os.ReadFile(j.path()); err != nil || !bytes.Equal(raw, original) {
		t.Fatal("canceled builder changed original", err)
	}
	fileCommit(t, j, req, ctx, rev, "selected")
	j.Close()
	if _, err := j.Read(ctx); err == nil {
		t.Fatal("closed read")
	}
	if err := j.Commit(ctx, "", nil); err == nil {
		t.Fatal("closed write")
	}
	if err := j.Close(); err != nil {
		t.Fatal("idempotent close", err)
	}
}

// OS process boundaries retain the directory lock until exit. Abrupt exit just
// after rename leaves a complete same-key record for reopen, not a second key.
// This process-interruption fixture does not simulate machine power loss.
func TestFileRepositoryProcessBoundary(t *testing.T) {
	if mode := os.Getenv("ROUTER_FILE_REPOSITORY_CHILD"); mode != "" {
		req := mustParse(t, []byte(goldenPrepare+"\n"), local)
		repo := FileRepository{os.Getenv("ROUTER_FILE_REPOSITORY_DIR")}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		j, err := repo.Open(ctx, req.Key())
		if mode == "busy" {
			if err == nil {
				j.Close()
				t.Fatal("parallel process admitted")
			}
			if !strings.Contains(err.Error(), "journal locked") {
				t.Fatal("wrong lock refusal", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		f := j.(*fileOperationJournal)
		f.step = func(stage string) error {
			if stage == "sync-directory" {
				os.Exit(23)
			}
			return nil
		}
		fileCommit(t, f, req, ctx, "", "resolving")
		t.Fatal("crash seam not reached")
	}
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	child := func(mode string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileRepositoryProcessBoundary$", "-test.count=1")
		cmd.Env = append(os.Environ(), "ROUTER_FILE_REPOSITORY_CHILD="+mode, "ROUTER_FILE_REPOSITORY_DIR="+repo.Directory)
		return cmd
	}
	cmd := child("busy")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-process lock: %v: %s", err, out)
	}
	t.Logf("busy fixture child PID %d reaped", cmd.Process.Pid)
	j.Close()
	cmd = child("crash")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("wrong crash exit %v: %s", err, out)
	}
	t.Logf("interrupted fixture child PID %d reaped", cmd.Process.Pid)
	j = openFileFixture(t, repo, req, ctx)
	raw, err := j.Read(ctx)
	if err != nil || raw == nil {
		t.Fatal("crash lost renamed record", err)
	}
	_, binding, ok := decodeOperation(raw, req.Key())
	if !ok || binding.Key() != req.Key() {
		t.Fatal("crash changed key")
	}
}

type diskMirrorRepository struct {
	repo FileRepository
	f    *operationFixture
}
type diskMirrorJournal struct {
	Journal
	f *operationFixture
}

func (r diskMirrorRepository) Open(ctx context.Context, key OperationKey) (Journal, error) {
	j, err := r.repo.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	r.f.key = key
	return diskMirrorJournal{j, r.f}, nil
}
func (j diskMirrorJournal) Read(ctx context.Context) ([]byte, error) {
	b, err := j.Journal.Read(ctx)
	j.f.raw = bytes.Clone(b)
	return b, err
}
func (j diskMirrorJournal) Commit(ctx context.Context, expected string, build func(string) ([]byte, error)) error {
	if err := j.Journal.Commit(ctx, expected, build); err != nil {
		return err
	}
	b, err := j.Read(ctx)
	j.f.raw = bytes.Clone(b)
	return err
}

// The real store docks with the frozen engine for start/reuse, reopened inert
// replay and same-operation Status. Lifecycle ports are fixtures: no real launch,
// source authenticity or current member readiness is claimed.
func TestFileRepositoryEngineDocking(t *testing.T) {
	for _, action := range []string{"start", "reuse"} {
		t.Run(action, func(t *testing.T) {
			repo, _, _ := fileFixture(t)
			f, e, r := newOperationFixture(t, action)
			e.Repository = diskMirrorRepository{repo, f}
			response(t, invocation(t, e, r), "receipt", "ready", "ok", 0)
			before := bytes.Clone(f.raw)
			pure := e
			pure.Planner, pure.Lifecycle, pure.Clock = nil, nil, nil
			x := response(t, invocation(t, pure, r), "receipt", "ready", "ok", 3)
			if x["historical"] != true || x["ready"] != nil || !bytes.Equal(before, f.raw) {
				t.Fatal("replay changed operation")
			}
			response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			if f.resolve != 1 || action == "start" && (f.start != 1 || f.prepare != 1) || action == "reuse" && (f.start != 0 || f.prepare != 0) {
				t.Fatal("duplicate lifecycle")
			}
		})
	}
}
