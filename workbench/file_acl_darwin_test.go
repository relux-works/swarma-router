package workbench

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// A real extended ACL on this isolated operation file is rejected despite mode
// 0600; its ACL-free neighbor is readable and rejection preserves exact bytes.
func TestFileRepositoryDarwinACL(t *testing.T) {
	repo, req, ctx := fileFixture(t)
	j := openFileFixture(t, repo, req, ctx)
	original, revision := fileCommit(t, j, req, ctx, "", "resolving")
	if _, err := j.Read(ctx); err != nil {
		t.Fatal("ACL-free control", err)
	}
	cmd := exec.CommandContext(ctx, "/bin/chmod", "+a", "everyone allow read,write", j.path())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ACL plant: %v %s", err, out)
	}
	t.Logf("ACL fixture child PID %d reaped", cmd.Process.Pid)
	info, err := os.Stat(j.path())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("ACL plant changed mode control", err)
	}
	if raw, err := j.Read(ctx); err == nil || raw != nil {
		t.Fatal("extended ACL accepted", err)
	}
	if err := j.Commit(ctx, revision, func(string) ([]byte, error) { t.Fatal("builder on ACL file"); return nil, nil }); err == nil {
		t.Fatal("ACL write accepted")
	}
	if after, err := os.ReadFile(j.path()); err != nil || !bytes.Equal(after, original) {
		t.Fatal("ACL record changed", err)
	}
}
