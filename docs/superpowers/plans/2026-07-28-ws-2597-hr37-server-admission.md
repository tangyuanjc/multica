# WS-2597 hr37 Server Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject every new, non-exempt engineering issue's first transition into `in_review` unless it contains a valid, completed hr37 assertion block.

**Architecture:** A pure `server/internal/issueguard` parser and admission policy owns the confirmed hr37 schema, rollout cutoff, and hardcoded exemption list. Create, single-update, and batch-update handlers call the policy before writing; batch updates use a read-only preflight so a violation cannot produce partial mutations.

**Tech Stack:** Go 1.26, Chi HTTP handlers, pgx/sqlc models, standard-library JSON parsing, PostgreSQL-backed handler tests.

---

### Task 1: Strict hr37 parser

**Files:**
- Create: `server/internal/issueguard/review_assertion_admission.go`
- Create: `server/internal/issueguard/review_assertion_admission_test.go`

- [ ] **Step 1: Write the failing parser tests**

Add table-driven cases for valid input, a missing key, duplicate keys, extra
keys, an unterminated mapping, multiple assertions, and marker text inside an
`evidence_cmd` string. The public test contract is:

```go
func TestReviewAssertionAdmissionParser(t *testing.T) {
	tests := []struct {
		name        string
		description string
		wantValid   bool
		wantCount   int
	}{
		{
			name:        "valid",
			description: `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}`,
			wantValid:   true,
			wantCount:   1,
		},
		{
			name:        "missing threshold",
			description: `assert_1: {evidence_cmd: "go test ./...", observed: "PASS"}`,
		},
		{
			name:        "quoted marker is inert",
			description: `assert_1: {evidence_cmd: "echo assert_2: inert", threshold: "exit 0", observed: "PASS"}`,
			wantValid:   true,
			wantCount:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseHR37Assertions(tt.description)
			if got.Valid() != tt.wantValid || len(got.Assertions) != tt.wantCount {
				t.Fatalf("ParseHR37Assertions() = %#v", got)
			}
		})
	}
}
```

- [ ] **Step 2: Run the parser test and verify RED**

Run: `cd server && go test ./internal/issueguard -run TestReviewAssertionAdmissionParser -count=1`

Expected: compile failure because `ParseHR37Assertions` does not exist.

- [ ] **Step 3: Implement the minimum strict parser**

Create these contracts:

```go
type HR37Assertion struct {
	Name            string
	EvidenceCommand string
	Threshold       string
	Observed        string
}

type HR37AssertionParseResult struct {
	Assertions []HR37Assertion
	HasMarkers bool
	Errors     []string
}

func (r HR37AssertionParseResult) Valid() bool {
	return r.HasMarkers && len(r.Assertions) > 0 && len(r.Errors) == 0
}

func ParseHR37Assertions(description string) HR37AssertionParseResult
```

The implementation scans `assert_<number>:` markers, extracts balanced mappings
while respecting string escapes, decodes values with `json.Decoder`, and accepts
exactly these keys:

```go
var requiredHR37Fields = []string{"evidence_cmd", "threshold", "observed"}
```

- [ ] **Step 4: Run the same parser test and verify GREEN**

- [ ] **Step 5: Commit**

```bash
git add server/internal/issueguard/review_assertion_admission.go server/internal/issueguard/review_assertion_admission_test.go
git commit -m "feat(issues): parse hr37 assertion blocks (WS-2597)"
```

### Task 2: Admission policy and auditable exemptions

**Files:**
- Modify: `server/internal/issueguard/review_assertion_admission.go`
- Modify: `server/internal/issueguard/review_assertion_admission_test.go`

- [ ] **Step 1: Write failing policy tests**

Cover missing assertions, blank `observed`, the rollout cutoff, a completed
block, leading daily/announcement titles, and a non-leading daily mention that
must not bypass.

```go
func TestReviewAssertionAdmissionPolicy(t *testing.T) {
	valid := `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}`
	tests := []struct {
		name  string
		input ReviewAssertionAdmissionInput
		want  ReviewAssertionAdmissionReason
		allow bool
	}{
		{name: "missing", input: ReviewAssertionAdmissionInput{Title: "Fix API"}, want: ReviewAssertionMissingBlock},
		{name: "valid", input: ReviewAssertionAdmissionInput{Title: "Fix API", Description: valid}, want: ReviewAssertionAllowed, allow: true},
		{name: "daily", input: ReviewAssertionAdmissionInput{Title: "[日报] 2026-07-28"}, want: ReviewAssertionExempt, allow: true},
		{name: "announcement", input: ReviewAssertionAdmissionInput{Title: "【公告】维护窗口"}, want: ReviewAssertionExempt, allow: true},
		{name: "later mention", input: ReviewAssertionAdmissionInput{Title: "[P1] 修复日报生成器"}, want: ReviewAssertionMissingBlock},
		{name: "legacy", input: ReviewAssertionAdmissionInput{Title: "Old", CreatedAt: HR37EnforcementStart.Add(-time.Second)}, want: ReviewAssertionGrandfathered, allow: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckReviewAssertionAdmission(tt.input)
			if got.Allowed != tt.allow || got.Reason != tt.want {
				t.Fatalf("CheckReviewAssertionAdmission() = %#v", got)
			}
		})
	}
}
```

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/issueguard -run TestReviewAssertionAdmissionPolicy -count=1`

- [ ] **Step 3: Implement the policy**

```go
var HR37EnforcementStart = time.Date(2026, time.July, 27, 21, 28, 17, 0, time.UTC)

type ReviewAssertionAdmissionInput struct {
	Identifier  string
	Title       string
	Description string
	CreatedAt   time.Time
}

type ReviewAssertionAdmissionResult struct {
	Allowed bool
	Reason  ReviewAssertionAdmissionReason
	Message string
}
```

Add reasons for allowed, grandfathered, exempt, missing, invalid, and blank
observed outcomes. Use a fixed list of leading bracket markers plus the exact
unbracketed prefix `🔍 Multica daily 扫描`. Every rejection message contains
`断言块` without echoing an `evidence_cmd`.

- [ ] **Step 4: Verify parser and policy GREEN**

Run: `cd server && go test ./internal/issueguard -run TestReviewAssertionAdmission -count=1`

- [ ] **Step 5: Commit**

```bash
git add server/internal/issueguard/review_assertion_admission.go server/internal/issueguard/review_assertion_admission_test.go
git commit -m "feat(issues): enforce hr37 review admission policy (WS-2597)"
```

### Task 3: Create and single-update enforcement

**Files:**
- Create: `server/internal/handler/issue_review_assertion_admission.go`
- Create: `server/internal/handler/issue_review_assertion_admission_test.go`
- Modify: `server/internal/handler/issue.go`

- [ ] **Step 1: Write failing integration tests**

The primary test creates a `todo` engineering issue without assertions, sends a
direct API transition to `in_review`, asserts HTTP 400 contains `断言块`, and
queries PostgreSQL to prove the status remains `todo`. Add cases for a completed
block, blank `observed`, daily/announcement exemptions, and direct create into
`in_review`.

- [ ] **Step 2: Verify RED**

```bash
set -a
source ../.env.worktree
set +a
go test ./internal/handler -run 'TestReviewAssertionAdmission(Create|Update|Whitelist)' -count=1
```

Expected: missing-block requests incorrectly return 200/201.

- [ ] **Step 3: Add handler helpers**

```go
func (h *Handler) admitExistingIssueToReview(
	w http.ResponseWriter,
	r *http.Request,
	issue db.Issue,
	title string,
	description string,
) bool

func admitNewIssueToReview(w http.ResponseWriter, title string, description string) bool
```

The existing-issue helper derives the workspace identifier and created time.
Both call `issueguard.CheckReviewAssertionAdmission` and use `writeError` on
rejection.

- [ ] **Step 4: Wire both handlers before mutation**

In `CreateIssue`, guard initial `in_review` after enum validation. In
`UpdateIssue`, compute prospective title/description and guard only when the
current status is not `in_review` and the requested status is `in_review`.

- [ ] **Step 5: Verify GREEN with the Step 2 command**

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/issue.go server/internal/handler/issue_review_assertion_admission.go server/internal/handler/issue_review_assertion_admission_test.go
git commit -m "feat(issues): gate review transitions on hr37 evidence (WS-2597)"
```

### Task 4: Batch preflight without partial writes

**Files:**
- Modify: `server/internal/handler/issue_review_assertion_admission.go`
- Modify: `server/internal/handler/issue_review_assertion_admission_test.go`
- Modify: `server/internal/handler/issue.go`

- [ ] **Step 1: Write a failing atomic-batch test**

Create one valid and one invalid `todo` issue. Batch-update both to `in_review`.
Assert HTTP 400 contains `断言块` and both database rows remain `todo`.

- [ ] **Step 2: Verify RED**

Run the handler package with `-run TestReviewAssertionAdmissionBatchPreflight`.

- [ ] **Step 3: Implement read-only preflight**

```go
func (h *Handler) preflightBatchReviewAssertionAdmission(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	issueIDs []string,
	updates UpdateIssueRequest,
) bool
```

For each parseable in-workspace ID, compute prospective title/description and
run the same guard. Preserve existing skip semantics for malformed, missing,
and cross-workspace IDs. Call the helper before `updated := 0` and before the
mutation loop whenever the requested status is `in_review`.

- [ ] **Step 4: Verify GREEN and run the full handler package**

```bash
set -a
source ../.env.worktree
set +a
go test ./internal/handler -run TestReviewAssertionAdmission -count=1
go test ./internal/handler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/issue.go server/internal/handler/issue_review_assertion_admission.go server/internal/handler/issue_review_assertion_admission_test.go
git commit -m "feat(issues): preflight hr37 batch admission (WS-2597)"
```

### Task 5: Regression, fork delivery, and local deployment

**Files:**
- Modify only if verification exposes an in-scope defect.

- [ ] **Step 1: Run the fixed regression**

```bash
set -a
source ../.env.worktree
set +a
go test ./internal/issueguard ./internal/handler -run 'Test.*ReviewAssertionAdmission' -count=1
```

- [ ] **Step 2: Run broader server verification**

Run both relevant packages without `-run`, then `go test ./... -count=1`. Report
the known `cmd/multica` task-marker failure separately if it reproduces; the
handler package must stay green.

- [ ] **Step 3: Format and inspect**

```bash
gofmt -w server/internal/issueguard/review_assertion_admission.go server/internal/issueguard/review_assertion_admission_test.go server/internal/handler/issue_review_assertion_admission.go server/internal/handler/issue_review_assertion_admission_test.go
git diff --check
git status --short
```

- [ ] **Step 4: Push only to the fork**

Push `agent/cto-codex/d7f67ba9` to `tangyuanjc/multica`. Do not open an upstream
`multica-ai/multica` PR and do not wait for upstream merge.

- [ ] **Step 5: Build and deploy locally**

Build the checkout's server with a fork version stamp. If an existing local
server service exists, preserve its binary, install the build, restart it, and
verify version/health. Otherwise launch this checkout's isolated self-host
stack and record its URL and health. Do not repoint the production cloud daemon
without an explicit migration plan.

- [ ] **Step 6: Record evidence**

Capture commit hashes, the fork branch URL, local deployed version/health, the
fixed regression output, and one non-blocking status snapshot if a fork-only PR
is opened for review.
