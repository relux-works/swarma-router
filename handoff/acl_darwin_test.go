package handoff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Real Darwin ACL plants on isolated fixtures must reject policy, directory,
// lock and receipt custody despite private mode bits. ACL-free controls work.
func TestDarwinExtendedACLCustody(t *testing.T) {
	for _, surface := range []string{"policy", "directory", "lock", "receipt"} {
		t.Run(surface, func(t *testing.T) {
			root := realTemp(t)
			policy := filepath.Join(root, "policy.json")
			if err := os.WriteFile(policy, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFile(policy, true); err != nil {
				t.Fatal("private control", err)
			}
			dir := filepath.Join(root, "journal")
			j, err := OpenJournal(dir)
			if err != nil {
				t.Fatal("journal control", err)
			}
			r, p := inputs()
			rec := Record{Contract: Contract, Request: r, PolicyDigest: hash(p), Notice: notice(r), Phase: "intent"}
			rec.IntentDigest = intentHash(r, rec.PolicyDigest, rec.Notice)
			if err := j.Save(r.CorrelationID, rec); err != nil {
				t.Fatal(err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
			if len(files) != 1 {
				t.Fatal(files)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			path := map[string]string{"policy": policy, "directory": dir, "lock": filepath.Join(dir, ".lock"), "receipt": files[0]}[surface]
			if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read,write", path).CombinedOutput(); err != nil {
				t.Fatalf("ACL plant failed: %v %s", err, out)
			}
			if surface == "policy" {
				if _, err := ReadFile(path, true); err == nil {
					t.Fatal("ACL policy accepted")
				}
				return
			}
			j, err = OpenJournal(dir)
			if surface != "receipt" {
				if err == nil {
					j.Close()
					t.Fatal("ACL journal/lock accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if _, _, err := j.Load(r.CorrelationID); err == nil {
				t.Fatal("ACL receipt loaded")
			}
			if err := j.Sync(r.CorrelationID); err == nil {
				t.Fatal("ACL receipt synced")
			}
		})
	}
}
