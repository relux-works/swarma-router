# Private local workbench storage

`workbench.FileRepository{Directory: "/absolute/existing/parent/operations"}`
implements the [operation engine's](workbench-operation.md) `Repository` port on
macOS and Linux. The trusted caller chooses one separate private directory out
of band and retains it for these operations. Request JSON cannot select a path.
There is no standalone storage CLI or automatic migration of notice/launch data.

The existing private-directory primitive admits an owned directory with no group
or other access, rejects symlink ancestors and extended macOS ACLs, takes a
nonblocking exclusive `.lock`, and syncs the existing parent. It may create one
private leaf; its parent must already exist. Trusted same-user ancestors and
cooperating writers are required. This is not protection against root, a hostile
same-user writer, directory replacement, or unsupported external deletion.

The lock lasts from `Open(ctx, key)` through `Close()`. It serializes all keys in
the directory, a stronger and less concurrent policy than per-key serialization.
A second writer gets a busy error immediately; it does not wait, retry or choose
another store. Keep each returned journal's lifetime bounded. No goroutine,
watchdog, process, session or live launch is started by the repository.

Each `workbench-v2-<hash>.json` name binds the canonical complete
caller/project/correlation tuple. Reads require an owned regular private file
with one hard link, no extended macOS ACL and at most 262144 bytes. The complete
closed operation record and embedded exact key are validated before use. Wrong
keys, corruption, unsupported formats, links and unreadable state are errors,
never absence. Only a missing exact file returns `nil,nil`. Historical notice and
launch filenames are not enumerated or opened.

`Commit` rereads under custody and compares the expected opaque revision before
calling the builder. Empty expected means reliably absent. The builder runs
once with a fresh random opaque revision. Returned bytes must contain that exact
revision and key and a valid full operation record; an existing operation must
retain its request digest, operation reference and immutable state. A first
commit must be the resolving reservation. A failed pre-write validation leaves
the prior bytes intact and does not invoke any lifecycle operation.

The write uses a private temporary file, checks the entire write, syncs and
closes it, atomically renames it to the exact record name, and syncs the directory
before acknowledging. There is no sidecar revision or second-file transaction.
Handled failures remove the unrenamed temporary file. A process exit can leave
an unused temporary file; the repository never treats it as an operation or
automatically deletes it. Existing records are never rolled back or reset.

After writes begin, any error makes that journal generation unusable for further
reads/commits. Close it and reconcile the original key through a fresh open.
An error after rename may have committed the new complete record. A fresh read
syncs the valid existing file and its directory before returning bytes, so an
unacknowledged rename is confirmed without rewriting it or executing again.
Same-key recovery and historical replay remain the engine's responsibility.

Context cancellation is checked before acquisition and between bounded local
filesystem operations, including after the builder and before acknowledgement.
The lock is nonblocking. OS filesystem syscalls, including `fsync`, are
synchronous and cannot be forcibly interrupted by a Go context; a deadline is
not a hard real-time bound on a stalled filesystem. No watchdog or fallback
storage is introduced. Use the admitted local filesystem and caller deadline.

Run `go test ./workbench -run '^TestFileRepository' -count=1 -v`,
`go test ./...`, `go vet ./...` and `go test -race ./...`.
The tests cover exact reopen/CAS, valid maximum-sized records and forbidden
neighbors, corrupt and transplanted keys, custody/ACL rejection, builder errors,
write/rename/sync acknowledgement boundaries, cancellation, actual process lock
exclusion and abrupt fixture-process exit after rename. Engine docking covers
start/reuse, reopened inert replay and original-key Status with fake lifecycle
ports. These checks do not simulate machine power loss or qualify a live tuple.
