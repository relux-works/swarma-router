# Optional read-only JSON assignment source

The queued handoff CLI normally reads its native task-board provider. An explicit
`json_board` object in the existing private policy selects a different, read-only
source. Omitting it preserves the native commands, validation, policy serialization
and journal digest. This does not install, configure, emulate or migrate a board.

The closed `keeper-assignment/1` file schema is illustrated by
[`assignment.json`](../examples/queued-handoff/json-board/assignment.json).
Its required string fields are `id`, `title`, `accountable_owner`, `assigned_to`,
`authority_event`, `created_at`, `scope`, and `status`. Status must be `ASSIGNED`,
the owner must equal the admitted sender, and the title and recipient must match
both the request and the private task binding. The authority event is syntax
checked; the admitted file hash binds its exact value. The adapter does not
independently authenticate that event or grant authority to a caller.

[`policy.json`](../examples/queued-handoff/json-board/policy.json) shows the
configuration. The existing optional `assignment_id` pattern admits the original
ID shape; the source never renames an assignment or hardcodes a deployment prefix.
Every admitted task has exactly one element and one recipient. The file must be
exactly `<id>.json`, a single component of at most 255 bytes. The policy pins the
SHA-256 of its complete original bytes. Editing even nonbinding metadata requires
explicit readmission of the new bytes. Unknown fields, duplicate or case-aliased
keys, invalid UTF-8, trailing values, missing fields, null binding fields and wrong
types refuse. Files are limited to 64 KiB and JSON nesting to 64 levels.

Files without project or room fields need explicit trusted context. Establish the
context from the actual board root and authenticated assignment metadata before
preparing the private policy. `context.project`, `project_root`, `board_dir`,
`room_id`, `topic` and `reply_to` must match their existing policy fields exactly.
The actual logical room alias is stored as `room_alias`, including its leading
`#`. Only that leading marker is omitted in the policy/request logical room slug:
`#example` maps to `example`, while the exact opaque room ID stays unchanged.
The adapter does not infer an alias from an opaque room ID, assignment title or
request. An absent context value returns `board_context_<field>_missing` before
source reads or messaging calls. Conflicting values refuse as well.

All source configuration belongs inside the privately admitted policy, including
the schema, context, file names, recipient and hashes. There is no environment,
CLI or unsigned companion override. The existing whole-policy journal digest
includes every new configuration field; legacy policies omit the new field
entirely and retain their original digest. Policy custody and authentication
remain the operator's existing responsibility, not a signature supplied by the
board adapter.

The board directory must be strictly below the admitted absolute, canonical
project root. Directory-FD traversal refuses symlinks in every component; leaf
opens are relative to that directory and never execute a board field. Project
and board roots must be owned by the current user and unwritable by group/other.
The regular, single-link element has the same ownership/write restrictions.
All three opened objects also pass the platform ACL custody check; an ACL or
a failed/unknown Darwin ACL query refuses. Checks and subsequent traversal/read
use the same descriptors, without reopening a checked path.
Trusted same-user ancestor custody is required, as for the existing policy;
this is not protection against a hostile same-user process or root. Missing,
changed, malformed or conflicting files stop before messaging. A second hash
check follows messaging reads, and another precedes an optional post.

After source admission the existing complete membership, caller, recipient,
reply sender, topic and exact room-ID checks still run. A saved membership or
reply snapshot does not become a live API response, and a topic response does not
satisfy the existing message-reply check. The adapter cannot disable these gates.
`plan` still returns only a notice and creates no journal. Dispatch still requires
the existing intent journal, post and event-readback checks; this source does not
establish acceptance or execution. Preparing configuration and passing synthetic
controls do not qualify a live plan.
