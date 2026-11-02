package workbench

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"relux.works/swarma-router/handoff"
)

// FileRepository uses one explicitly configured, separately owned private
// directory. Its nonblocking directory-wide lock serializes all cooperating
// writers (including different keys). Busy custody returns an error; it never
// waits, chooses another directory or reads notice/launch records.
// The directory is trusted configuration, never a field of a wire request.
type FileRepository struct{ Directory string }

func (r FileRepository) Open(ctx context.Context, key OperationKey) (Journal, error) {
	if err := fileContext(ctx); err != nil {
		return nil, err
	}
	if !ref(key.Caller) || !ref(key.Project) || !ref(key.CorrelationID) {
		return nil, errors.New("invalid operation key")
	}
	lock, err := handoff.OpenJournal(r.Directory)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(r.Directory)
	if err == nil {
		err = fileContext(ctx)
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return &fileOperationJournal{dir: abs, key: key, lock: lock}, nil
}

type fileOperationJournal struct {
	mu       sync.Mutex
	dir      string
	key      OperationKey
	lock     handoff.Journal
	closed   bool
	poisoned bool
	// An internal fault seam, never selectable by configuration or request.
	step func(string) error
}

func fileContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("missing context")
	}
	return ctx.Err()
}

func (j *fileOperationJournal) path() string {
	return filepath.Join(j.dir, "workbench-v2-"+hash(canonical(object(j.key)))+".json")
}

func (j *fileOperationJournal) check(ctx context.Context) error {
	if j.closed || j.poisoned {
		return errors.New("journal requires reopen")
	}
	return fileContext(ctx)
}

func (j *fileOperationJournal) at(ctx context.Context, stage string) error {
	if err := fileContext(ctx); err != nil {
		return err
	}
	if j.step != nil {
		return j.step(stage)
	}
	return nil
}

func (j *fileOperationJournal) syncDirectory(ctx context.Context, stage string) error {
	if err := j.at(ctx, stage); err != nil {
		return err
	}
	f, err := os.OpenFile(j.dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// read reestablishes durability of an existing complete record, including a
// previous writer's unacknowledged rename. Errors never become reliable absence.
// Custody assumes trusted same-user ancestors, not a hostile same-user writer.
func (j *fileOperationJournal) read(ctx context.Context) ([]byte, RetainedRequest, error) {
	f, err := os.OpenFile(j.path(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, RetainedRequest{}, fileContext(ctx)
	}
	if err != nil {
		return nil, RetainedRequest{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !privateOperationFile(info, f) || info.Size() > MaxRecordBytes {
		return nil, RetainedRequest{}, errors.New("invalid operation file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	if err != nil || len(b) > MaxRecordBytes {
		return nil, RetainedRequest{}, errors.New("operation file too large")
	}
	_, binding, ok := decodeOperation(b, j.key)
	if !ok {
		return nil, RetainedRequest{}, errors.New("invalid operation record")
	}
	if err := j.at(ctx, "read-sync-file"); err != nil {
		return nil, RetainedRequest{}, err
	}
	if err := f.Sync(); err != nil {
		return nil, RetainedRequest{}, err
	}
	if err := j.syncDirectory(ctx, "read-sync-directory"); err != nil {
		return nil, RetainedRequest{}, err
	}
	if err := fileContext(ctx); err != nil {
		return nil, RetainedRequest{}, err
	}
	return b, binding, nil
}

func privateOperationFile(info os.FileInfo, f *os.File) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid()) && st.Nlink == 1 && operationNoExtendedACL(f)
}

func (j *fileOperationJournal) Read(ctx context.Context) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(ctx); err != nil {
		return nil, err
	}
	b, _, err := j.read(ctx)
	return b, err
}

func (j *fileOperationJournal) Commit(ctx context.Context, expected string, build func(string) ([]byte, error)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(ctx); err != nil {
		return err
	}
	if build == nil || (expected != "" && !ref(expected)) {
		return errors.New("invalid commit")
	}
	old, binding, err := j.read(ctx)
	if err != nil {
		return err
	}
	if (old == nil && expected != "") || (old != nil && binding.DurableRevision() != expected) {
		return errors.New("revision conflict")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	revision := "revision-random:" + hex.EncodeToString(token[:])
	if revision == expected { // Fail closed even on an improbable random collision.
		return errors.New("revision collision")
	}
	if err := fileContext(ctx); err != nil {
		return err
	}
	b, err := build(revision)
	if err != nil {
		return err
	}
	b = bytes.Clone(b)
	next, nextBinding, ok := decodeOperation(b, j.key)
	if !ok || nextBinding.DurableRevision() != revision {
		return errors.New("invalid commit record")
	}
	if old != nil {
		prev, _, _ := decodeOperation(old, j.key)
		if !immutableTransition(prev, next) || binding.Digest() != nextBinding.Digest() || binding.OperationRef() != nextBinding.OperationRef() {
			return errors.New("invalid operation transition")
		}
	} else if !immutableTransition(operationRecord{}, next) {
		return errors.New("invalid initial operation")
	}
	if err := j.at(ctx, "create-temp"); err != nil {
		return err
	}
	f, err := os.CreateTemp(j.dir, ".workbench-v2-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	// Once writes begin, an error cannot be retried in this custody generation.
	// Close/reopen and the same key are required; no rollback/reset is attempted.
	j.poisoned = true
	if err := j.at(ctx, "write"); err != nil {
		return err
	}
	n, err := f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if err := j.at(ctx, "sync-file"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := j.at(ctx, "close-file"); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := j.at(ctx, "rename"); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), j.path()); err != nil {
		return err
	}
	if err := j.syncDirectory(ctx, "sync-directory"); err != nil {
		return err
	}
	if err := j.at(ctx, "acknowledge"); err != nil {
		return err
	}
	j.poisoned = false
	return nil
}

func (j *fileOperationJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.lock.Close()
}
