package launch

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"relux.works/swarma-router/handoff"
)

type fileStore struct {
	dir  string
	lock handoff.Journal
}

// OpenFileStore reuses the existing private-directory/ACL/parent-durability lock
// primitive, with distinct launch-* files and a separate explicitly owned directory.
// It never interprets, migrates or rewrites queued-notice records.
func OpenFileStore(dir string) (Store, error) {
	lock, err := handoff.OpenJournal(dir)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return &fileStore{abs, lock}, nil
}
func (s *fileStore) path(key string) string {
	return filepath.Join(s.dir, fmt.Sprintf("launch-%x.json", sha256.Sum256([]byte(key))))
}
func (s *fileStore) Close() error { return s.lock.Close() }
func (s *fileStore) Load(key string) (Record, bool, error) {
	b, err := handoff.ReadFile(s.path(key), true)
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var r Record
	if handoff.Decode(b, &r) != nil || !r.valid() || r.Intent.key() != key {
		return Record{}, true, errors.New("invalid launch record")
	}
	return r, true, nil
}
func (s *fileStore) Save(key string, r Record) error {
	if !r.valid() || r.Intent.key() != key {
		return errors.New("invalid launch record")
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > handoff.FileLimit {
		return errors.New("launch record too large")
	}
	f, err := os.CreateTemp(s.dir, ".launch-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path(key)); err != nil {
		return err
	}
	return syncDir(s.dir)
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *fileStore) Sync(key string) error {
	if _, _, err := s.Load(key); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(key), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return syncDir(s.dir)
}
