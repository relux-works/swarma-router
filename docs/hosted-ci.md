# Hosted Go checks

The Go CI workflow proposes validation for pull requests targeting `main` and
pushes to `main`. Pull requests check their exact head commit, rather than the
synthetic merge commit. The job logs the checked-out SHA. A passing head check
does not prove that a later merge with a different base passes.

One Ubuntu 24.04 job checks formatting, vets every package, and runs the full Go
test suite normally and with the race detector. Race checks cover the module's
concurrent policy reads, file repository operations and lifecycle contracts,
following their existing documented validation commands. Each test invocation
has a package timeout; the entire job has a 20-minute timeout. Shell and Go
failures fail the job. There are no path filters or ignored test failures.

The Go version comes from the full patch version in `go.mod` (currently 1.23.0).
The job checks the actual toolchain version and disables automatic toolchain
switching. Public dependencies come only from the exact module graph and
`go.sum`: download and checksum verification must succeed without changing
either module file. Remaining Go commands run with the module proxy disabled
and readonly module mode. The action's module/build cache restore and save are
disabled. Before setup-go, a shell step creates private job-local caches beneath
the runner-provided `RUNNER_TEMP` and exports their paths through `GITHUB_ENV`.
It requires an absolute, owned, writable directory without line breaks and uses
`umask 077` with a unique temporary directory. Runner data is read as quoted
shell variables; no pull request text is interpolated into that script.
The job-level environment contains no runner-context expressions, because
GitHub does not make the `runner` context available at that workflow key.
This does not pin the hosted OS image or the compiler used by the race detector.

The last step cross-compiles all packages and test binaries for Darwin arm64
with cgo disabled. It does **not** execute Darwin tests or establish native
macOS filesystem, extended ACL, process or runner qualification. Native Darwin
execution remains a separate check; cross-compilation is not a substitute.

The job uses only official actions pinned to full commit hashes, a GitHub-hosted
runner, and `contents: read`. Checkout does not persist its read credential;
setup-go requests no authentication token. No repository secrets, environment
secrets, deployment, repository writes, privileged PR events or self-hosted
runners are configured. Product tests use local fixtures and helper processes;
they do not authorize host credentials, a live Matrix session, room membership,
invitations, live replay or a runtime rollout.

This file describes proposed behavior, not a hosted run receipt. Runner and
repository Actions policy compatibility, action execution and the complete
hosted result remain unverified until an actual permitted run. Both pinned
actions require a runner supporting Node 24. Publishing this source does not
change repository settings or make the workflow a required branch check.
