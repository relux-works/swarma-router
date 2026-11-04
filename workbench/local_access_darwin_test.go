package workbench

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Real ACL plants on the policy and its private parent reject an otherwise
// valid mode-bit fixture. The adapter never repairs ACLs or changes policy bytes.
func TestLocalPolicyDarwinACL(t *testing.T) {
	for _, part := range []string{"file", "parent"} {
		t.Run(part, func(t *testing.T) {
			a, k, ctx, _ := accessFixture(t)
			accessReason(t, a.Read(ctx, k), "")
			before, err := os.ReadFile(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			path, mode := a.Path, os.FileMode(0600)
			if part == "parent" {
				path, mode = filepath.Dir(a.Path), 0700
			}
			cmd := exec.CommandContext(ctx, "/bin/chmod", "+a", "everyone allow read,write", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ACL plant: %v %s", err, out)
			}
			t.Logf("ACL fixture child PID %d reaped", cmd.Process.Pid)
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatal("ACL plant changed mode control", err)
			}
			accessReason(t, a.Read(ctx, k), "authority_unknown")
			after, err := os.ReadFile(a.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("policy bytes changed", err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if operationNoExtendedACL(f) {
				t.Fatal("rejection repaired or failed to plant ACL")
			}
		})
	}
}
