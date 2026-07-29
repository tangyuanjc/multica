# WS-2597 hr37 Server Admission Design

## Goal

Prevent newly created engineering issues from entering `in_review` unless their
description contains a valid, completed hr37 assertion block. Keep issue creation
available before the evidence exists, preserve pre-hr37 work, and exempt a small,
hardcoded set of non-engineering daily, patrol, and announcement families.

The confirmed hr37 block shape is:

```text
assert_1: {evidence_cmd: "...", threshold: "...", observed: "..."}
```

## Approaches considered

### 1. HTTP admission boundary plus a pure parser (selected)

Add a deterministic parser/classifier in `server/internal/issueguard`, then call
it before mutations in create, single-update, and batch-update handlers. Every
first-party and third-party client reaches those HTTP endpoints, while the pure
package keeps syntax, cutoff, and exemption policy independently testable.

This is the narrowest change that closes API/CLI/client bypasses, returns a clear
Chinese error containing `断言块`, and does not require a database migration.

### 2. PostgreSQL trigger

A trigger would cover every SQL writer, but parsing the hr37 inline mapping in
SQL would duplicate the confirmed parser semantics, make the exemption list hard
to review, and produce poor API errors. It would also move product policy into a
migration that is harder to iterate safely.

### 3. Full service-layer status-transition refactor

Centralizing every status write behind a new service method is attractive, but
the current update handlers call sqlc directly. Refactoring all status writers
would expand this issue into a platform-wide transition rewrite. The admission
guard can remain pure and be moved into such a service later without changing
its contract.

## Admission policy

The guard runs only when an issue would first enter `in_review`, including a
create request whose initial status is `in_review`. Creating an issue in another
status remains allowed without an assertion block.

An issue is admitted when any of these conditions is true:

1. It was created before the hr37 rollout instant
   `2026-07-27T21:28:17Z`.
2. Its title starts with an explicitly whitelisted non-engineering category.
3. Its description contains one or more valid, completed hr37 blocks.

The whitelist is code, not configuration. A leading bracketed category is
exempt only when its normalized text exactly equals one catalog entry:
`daily`, `work-daily`, `日报`, `工作日报`, `日报状态票`, `日报可见性`,
`loop radar daily`, `天猫投放监控日报`, `巡检`, `日检`, `周检`, `patrol`,
`公告`, or `announcement`. The known unbracketed automated prefix
`🔍 Multica daily 扫描` is also explicit and ASCII-case-insensitive. Exact
category matching prevents near-matches such as `[announcement-fix]` and an
engineering title such as `[P1] 修复日报生成器` from bypassing the gate.

Everything else is an engineering issue and must carry assertions.

## Assertion parser

The Go parser mirrors WS-2488's confirmed `mechanical_assertions.py` behavior:

- markers are named `assert_<number>:`;
- each marker must be followed by one balanced `{...}` inline mapping;
- the only allowed keys are `evidence_cmd`, `threshold`, and `observed`;
- all three values must be JSON strings;
- `evidence_cmd` and `threshold` must be nonblank;
- duplicate markers, duplicate keys, unexpected keys, malformed strings,
  unbalanced braces, and trailing commas are rejected;
- marker-like text inside a quoted value is inert;
- `observed` may be blank while drafting, but it must be nonblank on admission
  to `in_review`.

The parser treats command strings as data and never executes them.

## Request flow

### Create

`CreateIssue` validates the requested title and description before calling the
service when `status == "in_review"`. A missing or invalid block returns HTTP
400. The same payload with `todo`, `backlog`, or `in_progress` still creates the
issue.

### Single update

`UpdateIssue` computes the prospective title and description from the current
row plus any fields changed in the request. Every request explicitly targeting
`in_review` locks and reloads the row in a transaction; when that locked status
is non-review, the guard runs before the sqlc update in the same transaction. A
failure returns HTTP 400 and leaves the row unchanged. This closes stale-request
races while preserving redundant updates to rows already in review.

### Batch update

`BatchUpdateIssues` canonicalizes and deduplicates valid target IDs for locking,
locks them in deterministic UUID order in one transaction, and preflights every
resolvable in-scope locked row before applying any update to `in_review`. One
violation rejects the entire batch before the first mutation. Accepted writes
commit atomically, and events, task dispatch, and parent notification happen
only after commit. Invalid, unknown, or cross-workspace IDs preserve the
existing skip behavior; duplicate IDs retain request-order/count semantics.

All rejections include the issue identifier when available, the word `断言块`,
and a remediation hint naming the required fields.

## Error handling

- Missing assertions: explain that an engineering issue needs an hr37 `断言块`.
- Invalid syntax: explain that the block is invalid and include the first parser
  reason without echoing command contents.
- Blank observations: explain that every `observed` must be filled before
  review.
- Exempt and grandfathered issues pass without warnings or state changes.

## Verification

Pure unit tests cover valid, absent, malformed, duplicate, quoted-marker, cutoff,
and exemption behavior. Handler integration tests prove:

- direct API creation without a block succeeds outside `in_review`;
- the subsequent transition is rejected and the database status is unchanged;
- valid completed blocks pass;
- blank `observed` is rejected;
- a whitelisted daily/announcement issue passes;
- direct creation into `in_review` cannot bypass the guard;
- batch rejection is preflighted with zero partial status changes.

The fixed regression command is:

```bash
cd server && go test ./internal/issueguard ./internal/handler -run 'Test.*ReviewAssertionAdmission' -count=1
```

Delivery follows the existing organization decision: push the branch to
`tangyuanjc/multica`, do not open or wait on an upstream `multica-ai/multica`
merge, and build/deploy the fork locally. Local behavior, not upstream merge
state, is the acceptance signal.
