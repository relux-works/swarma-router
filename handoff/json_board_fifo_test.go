package handoff

import (
	"golang.org/x/sys/unix"
	"testing"
)

func makeBoardFIFO(t *testing.T, path string) {
	t.Helper()
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
}
