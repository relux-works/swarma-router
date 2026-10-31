package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A visible private leaf is not proof of parent durability. Both a failed first
// creation and an interrupted creation must refuse every retry until sync works;
// recovery must sync before one post, and replay must not post again.
func TestJournalParentSyncRecovery(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "creation-failure"
		if interrupted {
			name = "interrupted-visible-leaf"
		}
		t.Run(name, func(t *testing.T) {
			root := realTemp(t)
			dir := filepath.Join(root, "journal")
			if interrupted {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			fail := true
			attempts, successes := 0, 0
			syncFile := func(f *os.File) error {
				if f.Name() == root {
					attempts++
					if fail {
						return errors.New("injected parent sync failure")
					}
					if err := f.Sync(); err != nil {
						return err
					}
					successes++
					return nil
				}
				return f.Sync()
			}
			r, p := inputs()
			a := &fakeAdapter{}
			e := Engine{Adapter: a, OpenJournal: func(path string) (Journal, error) { return openJournal(path, syncFile) }}
			for i := 0; i < 2; i++ {
				got := e.Execute(context.Background(), r, p, dir)
				if got.Status != "refused" || got.Reason != "journal_parent_not_durable" || a.checks != 0 || a.posts != 0 || attempts != i+1 {
					t.Fatalf("PARENT_DURABILITY_BYPASS: %+v checks%d posts%d syncs%d", got, a.checks, a.posts, attempts)
				}
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
					t.Fatal("visible private leaf was not retained", err)
				}
			}
			fail = false
			syncedBeforePost := false
			a.beforePost = func() { syncedBeforePost = successes > 0 }
			for i := 0; i < 2; i++ {
				if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || got.EventID != "$sent" {
					t.Fatal(got)
				}
			}
			if !syncedBeforePost || successes != 2 || a.checks != 1 || a.posts != 1 || a.reads != 1 {
				t.Fatal("parent recovery/replay contract failed", successes, a)
			}
		})
	}
}

// Save really renames verified bytes before the injected directory sync fails.
// Reopen must retain unknown plus the known event while directory or receipt-file
// durability fails, and repair may return the historical receipt without a post.
func TestVisibleVerifiedSyncRecovery(t *testing.T) {
	root := realTemp(t)
	dir := filepath.Join(root, "journal")
	r, p := inputs()
	verifiedDirFails, receiptFileFails := true, false
	failedDirectorySyncs, successfulReceiptSyncs := 0, 0
	var recordPath string
	syncFile := func(f *os.File) error {
		if f.Name() == recordPath && recordPath != "" {
			if receiptFileFails {
				return errors.New("injected receipt sync failure")
			}
			if err := f.Sync(); err != nil {
				return err
			}
			successfulReceiptSyncs++
			return nil
		}
		if f.Name() == dir {
			files, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal("directory sync did not follow visible receipt rename", files, err)
			}
			recordPath = files[0]
			b, err := os.ReadFile(recordPath)
			var rec Record
			if err != nil || json.Unmarshal(b, &rec) != nil {
				t.Fatal("visible receipt unreadable", err)
			}
			if rec.Phase == "verified" && verifiedDirFails {
				failedDirectorySyncs++
				return errors.New("injected post-rename directory sync failure")
			}
		}
		return f.Sync()
	}
	a := &fakeAdapter{}
	e := Engine{Adapter: a, OpenJournal: func(path string) (Journal, error) { return openJournal(path, syncFile) }}
	got := e.Execute(context.Background(), r, p, dir)
	if got.Status != "unknown" || got.EventID != "$sent" || got.Reason != "verified_receipt_not_durable" || a.posts != 1 || failedDirectorySyncs != 1 {
		t.Fatal("visible-write fault not exercised", got, a, failedDirectorySyncs)
	}
	visible, err := os.ReadFile(recordPath)
	var rec Record
	if err != nil || Decode(visible, &rec) != nil || rec.Phase != "verified" || rec.EventID != "$sent" {
		t.Fatal("verified bytes were not installed before failure", err, rec)
	}
	for i := 0; i < 2; i++ {
		got = e.Execute(context.Background(), r, p, dir)
		if got.Status != "unknown" || got.EventID != "$sent" || got.Reason != "verified_receipt_not_durable" || a.posts != 1 || failedDirectorySyncs != i+2 {
			t.Fatalf("VISIBLE_VERIFIED_DURABILITY_BYPASS: %+v posts%d failedsyncs%d", got, a.posts, failedDirectorySyncs)
		}
	}
	verifiedDirFails, receiptFileFails = false, true
	got = e.Execute(context.Background(), r, p, dir)
	if got.Status != "unknown" || got.EventID != "$sent" || got.Reason != "verified_receipt_not_durable" || a.posts != 1 {
		t.Fatal("receipt-file sync failure became success", got, a)
	}
	receiptFileFails = false
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || got.EventID != "$sent" {
		t.Fatal("durability repair did not recover historical receipt", got)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil || string(after) != string(visible) || successfulReceiptSyncs != 3 || a.checks != 1 || a.posts != 1 || a.reads != 1 {
		t.Fatal("recovery rewrote receipt or repeated external effects", err, successfulReceiptSyncs, a)
	}
}
