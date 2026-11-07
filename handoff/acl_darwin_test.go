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

// These native plants exercise each JSON source surface independently. Cross
// compilation or an injected checker does not qualify native ACL behavior.
func TestDarwinJSONBoardExtendedACLCustody(t *testing.T) {
	for _, surface := range []string{"project", "board", "element"} {
		t.Run(surface, func(t *testing.T) {
			r, p, _ := jsonBoardInputs(t)
			if got := validateJSONBoardSource(r, p); got != "" {
				t.Fatal("ACL-free control", got)
			}
			path := map[string]string{"project": p.ProjectRoot, "board": p.BoardDir, "element": filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File)}[surface]
			// Clear the fixture ACL before TempDir cleanup, even on a test failure.
			t.Cleanup(func() {
				if out, err := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); err != nil {
					t.Errorf("ACL cleanup: %v %s", err, out)
				}
			})
			if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read,write", path).CombinedOutput(); err != nil {
				t.Fatalf("ACL plant: %v %s", err, out)
			}
			st, err := os.Stat(path)
			if err != nil || st.Mode().Perm()&0022 != 0 {
				t.Fatal("plant must retain private mode bits", st, err)
			}
			if got := validateJSONBoardSource(r, p); got != "board_element_unavailable" {
				t.Fatal("ACL write grant admitted", got)
			}
			if out, err := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); err != nil {
				t.Fatalf("clear ACL: %v %s", err, out)
			}
			if got := validateJSONBoardSource(r, p); got != "" {
				t.Fatal("restored ACL-free control", got)
			}
		})
	}
}

func TestDarwinACLQueryErrorRefuses(t *testing.T) {
	f, err := os.Open(realTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	if !noExtendedACL(f) {
		t.Fatal("ACL-free directory refused")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if noExtendedACL(f) {
		t.Fatal("failed descriptor query admitted")
	}
}
