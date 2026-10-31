package handoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const FileLimit = 64 * 1024

var errJournalParentNotDurable = errors.New("journal parent not durable")

type Record struct {
	Contract     string  `json:"contract"`
	Request      Request `json:"request"`
	PolicyDigest string  `json:"policy_digest"`
	IntentDigest string  `json:"intent_digest"`
	Notice       string  `json:"notice"`
	Phase        string  `json:"phase"`
	EventID      string  `json:"event_id,omitempty"`
}

func (r Record) validate() error {
	if r.Contract != Contract || r.Request.Contract != Contract || r.Request.Action != "dispatch" || !key.MatchString(r.Request.CorrelationID) || len(r.PolicyDigest) != 64 || len(r.IntentDigest) != 64 || r.Notice != notice(r.Request) || r.IntentDigest != intentHash(r.Request, r.PolicyDigest, r.Notice) {
		return errors.New("corrupt journal")
	}
	if _, err := hex.DecodeString(r.PolicyDigest); err != nil {
		return errors.New("corrupt journal")
	}
	switch r.Phase {
	case "intent":
		if r.EventID != "" {
			return errors.New("corrupt journal")
		}
	case "sent", "verified":
		if !eventID.MatchString(r.EventID) {
			return errors.New("corrupt journal")
		}
	case "unknown":
		if r.EventID != "" && !eventID.MatchString(r.EventID) {
			return errors.New("corrupt journal")
		}
	default:
		return errors.New("corrupt journal")
	}
	return nil
}

type Journal interface {
	Load(string) (Record, bool, error)
	Save(string, Record) error
	// Sync confirms the existing receipt file and its directory without rewriting it.
	Sync(string) error
	Close() error
}

type fileJournal struct {
	dir      string
	lock     *os.File
	syncFile func(*os.File) error
}

// OpenJournal takes a nonblocking lock. It never removes an uncertain receipt.
// Journal custody assumes trusted same-user ancestors, not hostile local root.
func OpenJournal(dir string) (Journal, error) {
	return openJournal(dir, (*os.File).Sync)
}

// The injected operation is only an internal filesystem fault seam; normal
// callers always use os.File.Sync, with no request-controlled durability mode.
func openJournal(dir string, syncFile func(*os.File) error) (Journal, error) {
	abs, err := filepath.Abs(dir)
	if err != nil || dir == "" {
		return nil, errors.New("invalid journal directory")
	}
	// Reject existing symlink ancestors before creating a missing directory.
	ancestor := abs
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(ancestor) == ancestor {
			return nil, errors.New("invalid journal ancestor")
		}
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil || resolved != ancestor {
		return nil, errors.New("symlink journal ancestor")
	}
	if ancestor != abs && filepath.Dir(abs) != ancestor {
		return nil, errors.New("journal parent must exist")
	}
	if ancestor != abs {
		if err := os.Mkdir(abs, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || !ownedPrivate(info) {
		return nil, errors.New("journal must be an owned private directory")
	}
	lock, err := os.OpenFile(filepath.Join(abs, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err = lock.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedPrivate(info) {
		lock.Close()
		return nil, errors.New("invalid journal lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("journal locked")
	}
	// Existence can follow a failed/interrupted Mkdir+parent sync. Reestablish
	// parent durability on every open, under the lock, before any dispatch work.
	if err := syncDirectory(filepath.Dir(abs), syncFile); err != nil {
		lock.Close()
		return nil, fmt.Errorf("%w: %v", errJournalParentNotDurable, err)
	}
	return &fileJournal{dir: abs, lock: lock, syncFile: syncFile}, nil
}

func syncDirectory(path string, syncFile func(*os.File) error) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = syncFile(d)
	closeErr := d.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func ownedPrivate(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0077 == 0
}

// ReadFile bounds file intake, refuses symlinks and optionally requires private custody.
func ReadFile(path string, private bool) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > FileLimit || (private && !ownedPrivate(info)) {
		return nil, errors.New("invalid input file")
	}
	b, err := io.ReadAll(io.LimitReader(f, FileLimit+1))
	if err != nil || len(b) > FileLimit {
		return nil, errors.New("input too large")
	}
	return b, nil
}

func (j *fileJournal) path(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(j.dir, fmt.Sprintf("%x.json", h))
}

func (j *fileJournal) Load(key string) (Record, bool, error) {
	b, err := ReadFile(j.path(key), true)
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var r Record
	if Decode(b, &r) != nil || r.validate() != nil || r.Request.CorrelationID != key {
		return Record{}, true, errors.New("corrupt journal")
	}
	return r, true, nil
}

func (j *fileJournal) Save(key string, r Record) error {
	if r.validate() != nil || r.Request.CorrelationID != key {
		return errors.New("invalid journal record")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > FileLimit {
		return errors.New("record too large")
	}
	f, err := os.CreateTemp(j.dir, ".receipt-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := j.syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, j.path(key)); err != nil {
		return err
	}
	return syncDirectory(j.dir, j.syncFile)
}

// Sync repairs visibility-without-confirmed-durability after a post-rename Save
// failure. A verified record is never cached success based on readable bytes alone.
func (j *fileJournal) Sync(key string) error {
	f, err := os.OpenFile(j.path(key), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedPrivate(info) || info.Size() > FileLimit {
		f.Close()
		return errors.New("invalid receipt for durability confirmation")
	}
	err = j.syncFile(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(j.dir, j.syncFile)
}

func (j *fileJournal) Close() error { return j.lock.Close() }
