package launch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Real private file storage retains and fsyncs the exact receipt across reopen;
// the journal lock refuses a parallel writer and never resets a corrupt record.
func TestFileStoreReceiptAndCustody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "launch-journal")
	real, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(real, "launch-journal")
	r, p, _, _ := routerFixture()
	r.OpenStore = func() (Store, error) { return OpenFileStore(dir) }
	i := intent()
	got := r.Start(context.Background(), i)
	if got.Status != "attempted" {
		t.Fatal(got)
	}
	files, err := filepath.Glob(filepath.Join(dir, "launch-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	before, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	got = r.Start(context.Background(), i)
	if got.Status != "attempted" || !got.Historical || p.starts != 1 {
		t.Fatal(got)
	}
	after, _ := os.ReadFile(files[0])
	if string(before) != string(after) {
		t.Fatal("historical receipt rewritten")
	}
	s, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, e := OpenFileStore(dir); e == nil {
		other.Close()
		t.Fatal("parallel writer admitted")
	}
	s.Close()
	if err := os.WriteFile(files[0], []byte(`{"contract":"corrupt"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got = r.Start(context.Background(), i)
	if got.Status != "unknown" || p.starts != 1 {
		t.Fatal(got)
	}
	corrupt, _ := os.ReadFile(files[0])
	if string(corrupt) != `{"contract":"corrupt"}` {
		t.Fatal("corruption erased")
	}
}

// Permissive directory mode and symlink records are refused, with an owned,
// private directory above as positive control. Existing notice journals are unused.
func TestFileStoreNegativeCustody(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "public")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Mkdir applies umask; establish the permissive input this refusal checks.
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("public directory fixture mode", info, err)
	}
	if s, e := OpenFileStore(dir); e == nil {
		s.Close()
		t.Fatal("public directory admitted")
	} else if e.Error() != "journal must be an owned private directory" {
		t.Fatal("unexpected custody refusal", e)
	}
	info, err = os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("refusal changed public directory mode", info, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("refusal wrote into public directory", entries, err)
	}
	dir = filepath.Join(parent, "private")
	s, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fs := s.(*fileStore)
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, fs.path(intent().key())); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(intent().key()); err == nil {
		t.Fatal("symlink receipt admitted")
	}
	s.Close()
}
