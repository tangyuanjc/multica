package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	completedReviewAssertionDescription = `assert_1: {evidence_cmd: "go test ./internal/handler", threshold: "exit status 0", observed: "exit status 0"}`
	blankObservedAssertionDescription   = `assert_1: {evidence_cmd: "go test ./internal/handler", threshold: "exit status 0", observed: " \t"}`
	invalidReviewAssertionDescription   = `assert_1: {evidence_cmd: "go test ./internal/handler", threshold: "exit status 0", observed: "exit status 0", sentinel_key: "must reject"}`
)

func reviewAssertionAdmissionTitle(label string) string {
	return fmt.Sprintf("[P1] HR37 handler %s %d", label, time.Now().UnixNano())
}

func createReviewAssertionAdmissionIssue(
	t *testing.T,
	title string,
	status string,
	description string,
) IssueResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       title,
		"status":      status,
		"description": description,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		deleteTestIssue(t, issue.ID)
	})
	return issue
}

func decodeReviewAssertionAdmissionError(
	t *testing.T,
	w *httptest.ResponseRecorder,
) string {
	t.Helper()

	var response struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, w.Body.String())
	}
	if response.Error == "" {
		t.Fatalf("error response contained an empty error field")
	}
	return response.Error
}

func TestReviewAssertionAdmissionCreateAllowsUnreviewedStatuses(t *testing.T) {
	for _, status := range []string{"todo", "backlog", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			title := reviewAssertionAdmissionTitle("create " + status)
			id := createTestIssue(t, title, status, "none")
			t.Cleanup(func() {
				deleteTestIssue(t, id)
			})

			var gotStatus string
			if err := testPool.QueryRow(
				context.Background(),
				`SELECT status FROM issue WHERE id = $1`,
				id,
			).Scan(&gotStatus); err != nil {
				t.Fatalf("reload created issue: %v", err)
			}
			if gotStatus != status {
				t.Fatalf("status = %q, want %q", gotStatus, status)
			}
		})
	}
}

func TestReviewAssertionAdmissionCreateInitialReview(t *testing.T) {
	t.Run("missing block is rejected without creating a row", func(t *testing.T) {
		title := reviewAssertionAdmissionTitle("initial review missing")
		t.Cleanup(func() {
			testPool.Exec(
				context.Background(),
				`DELETE FROM issue WHERE workspace_id = $1 AND title = $2`,
				testWorkspaceID,
				title,
			)
		})

		w := httptest.NewRecorder()
		req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":  title,
			"status": "in_review",
		})
		testHandler.CreateIssue(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("CreateIssue: expected 400, got %d: %s", w.Code, w.Body.String())
		}
		message := decodeReviewAssertionAdmissionError(t, w)
		if !strings.Contains(message, "断言块") ||
			!strings.Contains(message, "evidence_cmd") {
			t.Fatalf("CreateIssue: expected actionable assertion policy message, got %q", message)
		}
		if strings.HasPrefix(message, `issue "`) {
			t.Fatalf("new-issue rejection unexpectedly included an identifier prefix: %q", message)
		}

		var count int
		if err := testPool.QueryRow(
			context.Background(),
			`SELECT count(*) FROM issue WHERE workspace_id = $1 AND title = $2`,
			testWorkspaceID,
			title,
		).Scan(&count); err != nil {
			t.Fatalf("count rejected issue rows: %v", err)
		}
		if count != 0 {
			t.Fatalf("rejected create persisted %d issue rows, want 0", count)
		}
	})

	t.Run("completed block is allowed", func(t *testing.T) {
		title := reviewAssertionAdmissionTitle("initial review completed")
		issue := createReviewAssertionAdmissionIssue(
			t,
			title,
			"in_review",
			completedReviewAssertionDescription,
		)
		if issue.Status != "in_review" {
			t.Fatalf("created status = %q, want in_review", issue.Status)
		}
	})
}

func TestReviewAssertionAdmissionUpdateRejectsMissingBlockWithoutMutation(t *testing.T) {
	title := reviewAssertionAdmissionTitle("update missing")
	id := createTestIssue(t, title, "todo", "none")
	t.Cleanup(func() {
		deleteTestIssue(t, id)
	})
	var issueNumber int32
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT number FROM issue WHERE id = $1`,
		id,
	).Scan(&issueNumber); err != nil {
		t.Fatalf("load issue number: %v", err)
	}

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
			"status": "in_review",
		}),
		"id",
		id,
	)
	testHandler.UpdateIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	message := decodeReviewAssertionAdmissionError(t, w)
	if !strings.Contains(message, "断言块") ||
		!strings.Contains(message, "evidence_cmd") {
		t.Fatalf("UpdateIssue: expected actionable assertion policy message, got %q", message)
	}
	wantIdentityPrefix := fmt.Sprintf(`issue "HAN-%d"：`, issueNumber)
	if !strings.HasPrefix(message, wantIdentityPrefix) {
		t.Fatalf(
			"UpdateIssue error = %q, want derived identity prefix %q",
			message,
			wantIdentityPrefix,
		)
	}

	var status string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status FROM issue WHERE id = $1`,
		id,
	).Scan(&status); err != nil {
		t.Fatalf("reload rejected update: %v", err)
	}
	if status != "todo" {
		t.Fatalf("rejected update changed status to %q, want todo", status)
	}
}

func TestReviewAssertionAdmissionUpdateAllowsCompletedBlock(t *testing.T) {
	title := reviewAssertionAdmissionTitle("update completed")
	issue := createReviewAssertionAdmissionIssue(
		t,
		title,
		"todo",
		completedReviewAssertionDescription,
	)

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+issue.ID, map[string]any{
			"status": "in_review",
		}),
		"id",
		issue.ID,
	)
	testHandler.UpdateIssue(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status FROM issue WHERE id = $1`,
		issue.ID,
	).Scan(&status); err != nil {
		t.Fatalf("reload admitted update: %v", err)
	}
	if status != "in_review" {
		t.Fatalf("status = %q, want in_review", status)
	}
}

func TestReviewAssertionAdmissionUpdateRejectsBlankObservedWithoutMutation(t *testing.T) {
	title := reviewAssertionAdmissionTitle("blank observed")
	id := createTestIssue(t, title, "todo", "none")
	t.Cleanup(func() {
		deleteTestIssue(t, id)
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
			"status":      "in_review",
			"description": blankObservedAssertionDescription,
		}),
		"id",
		id,
	)
	testHandler.UpdateIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "断言块") ||
		!strings.Contains(w.Body.String(), "observed") {
		t.Fatalf("UpdateIssue: expected observed policy message, got %s", w.Body.String())
	}

	var status string
	var descriptionIsNull bool
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status, description IS NULL FROM issue WHERE id = $1`,
		id,
	).Scan(&status, &descriptionIsNull); err != nil {
		t.Fatalf("reload rejected update: %v", err)
	}
	if status != "todo" || !descriptionIsNull {
		t.Fatalf(
			"rejected update mutated row: status=%q description_is_null=%v",
			status,
			descriptionIsNull,
		)
	}
}

func TestReviewAssertionAdmissionUpdateRejectsInvalidBlockWithoutMutation(t *testing.T) {
	title := reviewAssertionAdmissionTitle("invalid syntax")
	id := createTestIssue(t, title, "todo", "none")
	t.Cleanup(func() {
		deleteTestIssue(t, id)
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
			"status":      "in_review",
			"description": invalidReviewAssertionDescription,
		}),
		"id",
		id,
	)
	testHandler.UpdateIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	message := decodeReviewAssertionAdmissionError(t, w)
	if !strings.Contains(message, "断言块") ||
		!strings.Contains(message, "格式无效") {
		t.Fatalf("UpdateIssue: expected invalid assertion policy message, got %q", message)
	}

	var status string
	var descriptionIsNull bool
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status, description IS NULL FROM issue WHERE id = $1`,
		id,
	).Scan(&status, &descriptionIsNull); err != nil {
		t.Fatalf("reload rejected update: %v", err)
	}
	if status != "todo" || !descriptionIsNull {
		t.Fatalf(
			"rejected invalid block mutated row: status=%q description_is_null=%v",
			status,
			descriptionIsNull,
		)
	}
}

func TestReviewAssertionAdmissionUpdateUsesProspectiveFields(t *testing.T) {
	t.Run("prospective title removes an existing exemption", func(t *testing.T) {
		oldTitle := fmt.Sprintf("[日报] HR37 prospective title %d", time.Now().UnixNano())
		newTitle := reviewAssertionAdmissionTitle("prospective engineering title")
		id := createTestIssue(t, oldTitle, "todo", "none")
		t.Cleanup(func() {
			deleteTestIssue(t, id)
		})

		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
				"title":  newTitle,
				"status": "in_review",
			}),
			"id",
			id,
		)
		testHandler.UpdateIssue(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("UpdateIssue: expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "断言块") {
			t.Fatalf("UpdateIssue: expected assertion policy message, got %s", w.Body.String())
		}

		var status string
		var persistedTitle string
		if err := testPool.QueryRow(
			context.Background(),
			`SELECT status, title FROM issue WHERE id = $1`,
			id,
		).Scan(&status, &persistedTitle); err != nil {
			t.Fatalf("reload rejected update: %v", err)
		}
		if status != "todo" || persistedTitle != oldTitle {
			t.Fatalf(
				"rejected update mutated row: status=%q title=%q",
				status,
				persistedTitle,
			)
		}
	})

	t.Run("same-request description completion is admitted", func(t *testing.T) {
		title := reviewAssertionAdmissionTitle("prospective description")
		id := createTestIssue(t, title, "todo", "none")
		t.Cleanup(func() {
			deleteTestIssue(t, id)
		})

		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
				"status":      "in_review",
				"description": completedReviewAssertionDescription,
			}),
			"id",
			id,
		)
		testHandler.UpdateIssue(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var status string
		var description string
		if err := testPool.QueryRow(
			context.Background(),
			`SELECT status, description FROM issue WHERE id = $1`,
			id,
		).Scan(&status, &description); err != nil {
			t.Fatalf("reload admitted update: %v", err)
		}
		if status != "in_review" || description != completedReviewAssertionDescription {
			t.Fatalf(
				"admitted update did not persist prospective fields: status=%q description=%q",
				status,
				description,
			)
		}
	})
}

func TestReviewAssertionAdmissionWhitelistAllowsMissingBlock(t *testing.T) {
	for _, prefix := range []string{"[日报]", "【公告】"} {
		t.Run(prefix+"/update", func(t *testing.T) {
			title := fmt.Sprintf("%s HR37 update whitelist %d", prefix, time.Now().UnixNano())
			id := createTestIssue(t, title, "todo", "none")
			t.Cleanup(func() {
				deleteTestIssue(t, id)
			})

			w := httptest.NewRecorder()
			req := withURLParam(
				newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{
					"status": "in_review",
				}),
				"id",
				id,
			)
			testHandler.UpdateIssue(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var status string
			if err := testPool.QueryRow(
				context.Background(),
				`SELECT status FROM issue WHERE id = $1`,
				id,
			).Scan(&status); err != nil {
				t.Fatalf("reload whitelisted update: %v", err)
			}
			if status != "in_review" {
				t.Fatalf("status = %q, want in_review", status)
			}
		})

		t.Run(prefix+"/create", func(t *testing.T) {
			title := fmt.Sprintf("%s HR37 create whitelist %d", prefix, time.Now().UnixNano())
			issue := createReviewAssertionAdmissionIssue(t, title, "in_review", "")
			if issue.Status != "in_review" {
				t.Fatalf("created status = %q, want in_review", issue.Status)
			}
		})
	}
}

func TestReviewAssertionAdmissionUpdateDoesNotRegateAlreadyInReview(t *testing.T) {
	oldTitle := fmt.Sprintf("[日报] HR37 already review %d", time.Now().UnixNano())
	newTitle := reviewAssertionAdmissionTitle("already review engineering")
	issue := createReviewAssertionAdmissionIssue(t, oldTitle, "in_review", "")

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+issue.ID, map[string]any{
			"title":  newTitle,
			"status": "in_review",
		}),
		"id",
		issue.ID,
	)
	testHandler.UpdateIssue(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	var title string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status, title FROM issue WHERE id = $1`,
		issue.ID,
	).Scan(&status, &title); err != nil {
		t.Fatalf("reload re-sent review update: %v", err)
	}
	if status != "in_review" || title != newTitle {
		t.Fatalf("re-sent review update not persisted: status=%q title=%q", status, title)
	}
}
