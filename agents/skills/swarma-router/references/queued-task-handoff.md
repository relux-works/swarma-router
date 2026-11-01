# Queued task notice MVP

## Supported interface and readiness

`queued-task-handoff/0.1.0` is the reduced Go CLI/library at reviewed source `74f240cfe936d153665639afa4bf585850e16cb7`. It validates an explicit task/destination, saves intent before posting through the caller's existing identity, and verifies the server event. A Darwin arm64 artifact from that source was built, reviewed and exercised through plan, one dispatch, matching replay and complete server readback on 2026-10-31. That establishes the exercised tuple only. Linux runtime use, arbitrary environments and the full Router interfaces are not thereby qualified.

The executable name is `queued-task-handoff`; use the exact supplied reviewed artifact path. There is no assumed globally installed `swarma-router` command. Source build/test commands and implementation limits are maintained in the [source README at this revision](https://github.com/relux-works/swarma-router/blob/74f240cfe936d153665639afa4bf585850e16cb7/README.md). This skill candidate supplies no executable, installer, credentials, identity, room or runtime admission.

This MVP sends a **queued notification**. It does not choose a member/model, read a product card, inspect liveness, launch a session, create a claim or mutate an assignment. Select the authorized recipient explicitly. Do not represent this as the proposed full Route/Apply or plugin interface.

## Prepare the exact local inputs

Use the existing authorized local project and messaging configuration, with trusted `a2a` and `task-board` executables on PATH. The adapter supports a local board; a board directory selects storage, not assignment authority. Confirm that the task reference and nonsecret title may be shared with this recipient/room. A normal source read is not authority to post. Retain any existing owner/resource boundaries for actual tool execution.

The request has exactly these case-sensitive fields; this complete example is illustrative and cannot be dispatched as live evidence:

```json
{
  "contract": "queued-task-handoff/0.1.0",
  "action": "plan",
  "correlation_id": "example-not-live-1",
  "project": "example",
  "task_id": "TASK-261031-example",
  "task_title": "TASK-261031-example: Example task",
  "member": "recipient",
  "room": "example",
  "topic": "work",
  "reply_to": "$example-not-live"
}
```

The separate local policy has exactly these fields, with a nonempty allowlist:

```json
{
  "contract": "queued-task-handoff/0.1.0",
  "project": "example",
  "project_root": "/absolute/path/to/project",
  "board_dir": "/absolute/path/to/project/.task-board",
  "server": "example.test",
  "sender": "caller",
  "room": "example",
  "room_id": "!example-not-live",
  "topic": "work",
  "reply_to": "$example-not-live",
  "tasks": [
    {
      "id": "TASK-261031-example",
      "title": "TASK-261031-example: Example task",
      "members": ["recipient"]
    }
  ]
}
```

Replace illustrative values using independently verified actual references. Copy the exact native task title, including its existing ID prefix; do not derive a friendlier title from the task ID. Bind the actual project root, local board storage, server, sender, room ID, topic, reply event and allowed task/member. Use the current caller's identity only; no token, environment override, free-form task body, command or secret is accepted in the request. The policy is operator-controlled local admission, not cryptographic authority or proof of assignment. Keep it owned by the current user, mode 0600, in trusted custody.

The MVP's `sender` and `member` fields are local account names, bound to the policy's single exact server when checking membership. They do not accept a full federated MXID. Do not strip a foreign homeserver or treat equal local names on different servers as the same recipient; a federated destination needs a separately supported contract/admission.

Both JSON files are limited to 64 KiB. Unknown/duplicate keys, case aliases and trailing JSON are rejected. The policy allows at most 256 tasks and 64 distinct recipients per task. These constraints do not make fictitious references valid. Required source checks verify the actual board/task/title, complete joined caller/recipient membership, server and room identity, and the existing reply event's room/topic/joined sender.

## Plan before the separately authorized dispatch

The CLI requires all three flags. Set task-specific absolute paths; `ROUTER_MVP` below is the supplied artifact, not a download or discovery command:

```sh
ROUTER_MVP=/absolute/path/to/reviewed/queued-task-handoff
REQUEST_PLAN=/absolute/path/to/plan.json
POLICY=/absolute/path/to/private-policy.json
JOURNAL=/absolute/existing/parent/task-handoff-journal
"$ROUTER_MVP" --request "$REQUEST_PLAN" --policy "$POLICY" --journal "$JOURNAL"
```

`plan` checks sources and returns the proposed notice with `planned`, exit 0. It creates no journal and posts nothing. A successful plan does not reserve capacity, freeze source state or authorize the dispatch. Before dispatch, obtain the applicable authorization for this exact recipient/task/destination if not already given; do not ask again when it already covers the operation.

Prepare a separate complete request file by changing only `action` to `dispatch`, preserving the intended correlation and all other fields. Do not pass `--action`, `--force`, `--retry` or a full Router operation envelope; those are not supported. The next command is the effectful step, executed only inside the admitted scope/window:

```sh
REQUEST_DISPATCH=/absolute/path/to/dispatch.json
"$ROUTER_MVP" --request "$REQUEST_DISPATCH" --policy "$POLICY" --journal "$JOURNAL"
```

The journal parent must already exist at a real path without symlink ancestors. The CLI creates a private leaf and holds its nonblocking lock through the operation. Reuse this journal for these correlation IDs; never create a new journal to escape a previous result. Dispatch validates current sources before a new post, so a stale plan does not bypass changed membership or task state.

## Interpret the result before another action

| JSON status | Exit | Meaning and allowed conclusion |
| --- | ---: | --- |
| `planned` | 0 | Plan checks passed; notice prepared, zero messages sent. |
| `queued_notification` | 0 | Exact event verified now, or a matching historical verified receipt returned. Record correlation/event ID and distinguish these cases. |
| `refused` | 2 | This invocation rejected input, source or custody before a new post attempt. Preserve the bounded reason; this does not prove an earlier invocation sent nothing. |
| `conflict` | 2 | Same correlation belongs to a different request/policy. Existing intent is preserved; do not bypass it with a new key. |
| `unknown` | 3 | A post may have happened. Preserve the journal, same correlation and any known event ID for reconciliation. Do not dispatch again blindly. |

The result includes `contract`, `status`, `correlation_id`, plus `event_id`, `notice` and bounded `reason` when applicable. Empty/malformed output, an interrupted caller or an unknown exit does not prove no send. Native subprocess deadlines are 30s each, with bounded stdout/stderr; a caller timeout ends waiting, not the published effect.

A matching verified replay confirms receipt durability and returns the **historical same event** without reposting or making new source checks. It is not fresh membership, permission or current task-state evidence. Replaying an unresolved intent with no saved event remains unknown and does not post. With a durably saved event, matching reconciliation may read that event and complete verification without reposting. A durability/readback failure remains unknown, retaining any known event ID. Use a supported replay/reconciliation only when that actual diagnostic invocation is authorized; it is not a general retry instruction.

Server publication, recipient receipt, accepted assignment, work started and completed work are separate facts. Obtain recipient acknowledgement through the ordinary admitted communication route when the requested outcome needs it; the CLI does not supply that acknowledgement. Local journal/locking provides no server-side exactly-once guarantee or protection against journal loss and use of another directory. Same-user tool/configuration races remain outside its trust boundary.

## Preserve uncertain effects and rollback

Do not edit/delete an uncertain intent, change its correlation, copy it into a different journal or manually import an event to force success. The CLI has no cancel, force-retry, journal-reset or full Router Status command. Preserve the original file and known references; ask the existing messaging custodian to reconcile any unresolved server effect through its admitted route, sharing only the needed nonsecret binding. Do not borrow an identity, inspect foreign private stores or retract messages without authority.

Rollback stops future invocations and restores the previously reviewed artifact through its normal custody path. Retain the journal and any event ID. It does not retract a posted notice, undo recipient activity, stop a session or change the board.

## Paired checks for this guide

These are review scenarios grounded in the implemented MVP contract, not newly executed tests. Existing source tests cover corresponding failure classes; the successful real-use tuple does not qualify every scenario or future environment.

| Scenario | Nearby valid control | Forbidden result / required boundary |
| --- | --- | --- |
| Exact schema and real title | Complete plan with the exact native prefixed task title and admitted tuple can return planned with no journal/post. | A full Route envelope, alias `Action`, duplicate key or mismatched title cannot become a dispatch. Distinguish parser rejection from actual task-source refusal. |
| Current destination/member | A currently joined admitted caller/recipient and matching existing reply can proceed under the policy. | Missing membership, wrong topic/room/reply or unlisted member cannot cause a post. A previous successful plan does not override drift. |
| Matching receipt | Same dispatch request, policy and original verified journal returns the same event without repost. | Changed member/task/policy under the same correlation cannot be treated as a fresh notice or repaired by changing the key. |
| Lost post/readback | A complete exact event readback can establish queued_notification. | A post whose result/readback is lost stays unknown; no second post, invented no-send or recipient acceptance. |
| Interrupted durability | Successful parent/record sync allows the normal once-post and historical receipt paths. | Visible journal creation or verified-file rename after failed sync does not alone establish durability or cached success. Preserve unknown/known event on receipt-sync failure. |
| Scope of successful use | Queued event plus independent recipient acknowledgement establishes those two facts for that tuple. | No automatic assignment/claim/launch/liveness/work-completion or full Router conformance can be inferred. |

## Immutable policies and custody

Keep an immutable private policy snapshot for each task and authorization revision, and retain the
original dispatch request beside it. New tasks get new policy files and reuse the same journal.
The contract intentionally hashes the whole policy: adding an unrelated task changes the intent,
including for records with a known event that still require reconciliation. Supply the original
policy to reconcile; never erase a record, invent a new key or change journals to bypass conflict.
No implicit receipt migration occurs in this implementation revision.

On macOS, extended ACLs on policy/journal/lock/receipt objects are refused, including objects whose
mode bits are 0600/0700. Keep ancestors under trusted custody as well as free of symlinks. JSON
nesting is limited to 64 containers, and notice text rejects controls, format characters, Unicode
line/paragraph separators, private-use characters, surrogates and noncharacters. Missing or malformed
result JSON is uncertainty even if the native process exit happens to be 2.
