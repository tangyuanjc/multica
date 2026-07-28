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

func TestGetIssuePulseReturnsConservedPrivacySafeProjection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	reviewerID := createHandlerTestAgent(t, "Pulse Reviewer", nil)
	workerID := createHandlerTestAgent(t, "Pulse Worker", nil)

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (
			workspace_id, title, description, assignee_type, assignee_id, status,
			execution_mode, issue_title_template, created_by_type, created_by_id, last_run_at
		) VALUES ($1, 'Pulse Daily', 'private prompt text', 'agent', $2, 'active',
			'create_issue', 'private {{template}}', 'member', $3, now())
		RETURNING id
	`, testWorkspaceID, workerID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("insert autopilot: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, autopilotID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO autopilot_trigger (autopilot_id, kind, enabled, cron_expression, webhook_token, signing_secret)
		VALUES ($1, 'schedule', true, '0 9 * * *', 'private-webhook', 'private-secret')
	`, autopilotID); err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status, triggered_at, completed_at, trigger_payload, result)
		VALUES ($1, 'schedule', 'completed', now() - interval '5 minutes', now() - interval '4 minutes', '{"private":"payload"}', '{"private":"result"}')
	`, autopilotID); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	createdAt := time.Now().UTC().Add(-30 * time.Minute)
	insertIssue := func(status, title string) string {
		t.Helper()
		var number int
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
			WHERE id = $1 RETURNING issue_counter
		`, testWorkspaceID).Scan(&number); err != nil {
			t.Fatalf("next issue number: %v", err)
		}
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, status, priority, assignee_type, assignee_id,
				creator_type, creator_id, number, origin_type, origin_id, created_at
			) VALUES ($1, $2, $3, 'none', 'agent', $4, 'agent', $4, $5, 'autopilot', $6, $7)
			RETURNING id
		`, testWorkspaceID, title, status, workerID, number, autopilotID, createdAt).Scan(&id); err != nil {
			t.Fatalf("insert issue: %v", err)
		}
		return id
	}
	first := insertIssue("done", "pulse-first")
	reworked := insertIssue("done", "pulse-reworked")
	cancelled := insertIssue("cancelled", "pulse-cancelled")
	inFlight := insertIssue("in_review", "pulse-inflight")

	insertStatus := func(issueID, actorType, actorID, from, to string, at time.Time) {
		t.Helper()
		if _, err := testPool.Exec(ctx, `
			INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
			VALUES ($1, $2, $3, NULLIF($4, '')::uuid, 'status_changed', jsonb_build_object('from', $5::text, 'to', $6::text), $7)
		`, testWorkspaceID, issueID, actorType, actorID, from, to, at); err != nil {
			t.Fatalf("insert status: %v", err)
		}
	}
	insertStatus(first, "agent", workerID, "in_progress", "in_review", createdAt.Add(5*time.Minute))
	insertStatus(first, "agent", reviewerID, "in_review", "done", createdAt.Add(10*time.Minute))
	insertStatus(reworked, "agent", workerID, "in_progress", "in_review", createdAt.Add(5*time.Minute))
	insertStatus(reworked, "agent", reviewerID, "in_review", "in_progress", createdAt.Add(8*time.Minute))
	insertStatus(reworked, "agent", workerID, "in_progress", "in_review", createdAt.Add(12*time.Minute))
	insertStatus(reworked, "agent", reviewerID, "in_review", "done", createdAt.Add(20*time.Minute))
	insertStatus(inFlight, "agent", workerID, "in_progress", "in_review", createdAt.Add(15*time.Minute))

	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'human touched')
	`, reworked, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	since := createdAt.Add(-time.Minute).Format(time.RFC3339)
	until := createdAt.Add(time.Hour).Format(time.RFC3339)
	path := fmt.Sprintf("/api/issues/pulse?reviewer_id=%s&since=%s&until=%s", reviewerID, since, until)
	recorder := httptest.NewRecorder()
	testHandler.GetIssuePulse(recorder, newRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GetIssuePulse: got %d: %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		SchemaVersion string `json:"schema_version"`
		S2            struct {
			FirstPass      int  `json:"first_pass"`
			Reworked       int  `json:"reworked"`
			Cancelled      int  `json:"cancelled"`
			InFlight       int  `json:"in_flight"`
			Total          int  `json:"total"`
			ConservationOK bool `json:"conservation_ok"`
		} `json:"s2"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SchemaVersion != "issue-pulse.v1" || !body.S2.ConservationOK || body.S2.Total != 4 || body.S2.FirstPass != 1 || body.S2.Reworked != 1 || body.S2.Cancelled != 1 || body.S2.InFlight != 1 {
		t.Fatalf("unexpected response: %#v", body)
	}
	serialized := recorder.Body.String()
	for _, forbidden := range []string{"private prompt text", "private {{template}}", "private-webhook", "private-secret", "trigger_payload", `"result":`} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("response leaked forbidden field/value %q: %s", forbidden, serialized)
		}
	}
	_ = cancelled
}

func TestGetIssuePulseRequiresReviewerID(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler.GetIssuePulse(recorder, newRequest(http.MethodGet, "/api/issues/pulse", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}
