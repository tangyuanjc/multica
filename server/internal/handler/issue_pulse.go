package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/pulse"
)

const pulseDefaultWindow = 30 * 24 * time.Hour

// GetIssuePulse returns a read-only S1-S5 aggregation for autopilot-origin
// issues in the requested creation cohort. reviewer_id is deliberately an
// input: organization identities never belong in the server binary.
func (h *Handler) GetIssuePulse(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	reviewerID := strings.TrimSpace(r.URL.Query().Get("reviewer_id"))
	if reviewerID == "" {
		writeError(w, http.StatusBadRequest, "reviewer_id is required")
		return
	}
	reviewerUUID, ok := parseUUIDOrBadRequest(w, reviewerID, "reviewer_id")
	if !ok {
		return
	}
	since, until, err := parsePulseWindow(r, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var reviewerExists bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM agent WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL
		)
	`, reviewerUUID, workspaceUUID).Scan(&reviewerExists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate reviewer")
		return
	}
	if !reviewerExists {
		writeError(w, http.StatusBadRequest, "reviewer_id is not an active agent in this workspace")
		return
	}

	issues, err := h.loadPulseIssues(r, workspaceUUID, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pulse issues")
		return
	}
	events, err := h.loadPulseStatusEvents(r, workspaceUUID, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pulse status events")
		return
	}
	loops, err := h.loadPulseLoops(r, workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pulse loops")
		return
	}

	writeJSON(w, http.StatusOK, pulse.Aggregate(pulse.Input{
		ReviewerID:   reviewerID,
		Since:        since,
		Until:        until,
		Issues:       issues,
		StatusEvents: events,
		Loops:        loops,
	}))
}

func parsePulseWindow(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	until := now
	if raw := strings.TrimSpace(r.URL.Query().Get("until")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("until must be RFC3339")
		}
		until = parsed
	}
	since := until.Add(-pulseDefaultWindow)
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("since must be RFC3339")
		}
		since = parsed
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, fmt.Errorf("since must be before until")
	}
	return since.UTC(), until.UTC(), nil
}

func (h *Handler) loadPulseIssues(r *http.Request, workspaceID pgtype.UUID, since, until time.Time) ([]pulse.Issue, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT
			i.id::text,
			i.origin_id::text,
			a.title,
			i.status,
			i.creator_type,
			COALESCE(i.assignee_type, ''),
			i.created_at,
			(
				SELECT count(*)::int
				FROM agent_task_queue task
				WHERE task.issue_id = i.id AND task.rerun_of_task_id IS NOT NULL
			),
			(
				EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.author_type = 'member')
				OR EXISTS (
					SELECT 1 FROM activity_log activity
					WHERE activity.issue_id = i.id
					  AND (
						activity.actor_type = 'member'
						OR (activity.action = 'assignee_changed' AND activity.details->>'to_type' = 'member')
					  )
				)
			)
		FROM issue i
		JOIN autopilot a ON a.id = i.origin_id AND a.workspace_id = i.workspace_id
		WHERE i.workspace_id = $1
		  AND i.origin_type = 'autopilot'
		  AND i.created_at >= $2
		  AND i.created_at < $3
		ORDER BY i.created_at, i.id
	`, workspaceID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	issues := []pulse.Issue{}
	for rows.Next() {
		var issue pulse.Issue
		if err := rows.Scan(
			&issue.ID, &issue.LoopID, &issue.LoopTitle, &issue.Status,
			&issue.CreatorType, &issue.AssigneeType, &issue.CreatedAt,
			&issue.RerunCount, &issue.HumanTouched,
		); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func (h *Handler) loadPulseStatusEvents(r *http.Request, workspaceID pgtype.UUID, since, until time.Time) ([]pulse.StatusEvent, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT
			activity.issue_id::text,
			COALESCE(activity.actor_type, ''),
			COALESCE(activity.actor_id::text, ''),
			COALESCE(activity.details->>'from', ''),
			COALESCE(activity.details->>'to', ''),
			activity.created_at
		FROM activity_log activity
		JOIN issue i ON i.id = activity.issue_id
		WHERE i.workspace_id = $1
		  AND i.origin_type = 'autopilot'
		  AND i.created_at >= $2
		  AND i.created_at < $3
		  AND activity.action = 'status_changed'
		  AND activity.created_at < $3
		ORDER BY activity.issue_id, activity.created_at, activity.id
	`, workspaceID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []pulse.StatusEvent{}
	for rows.Next() {
		var event pulse.StatusEvent
		if err := rows.Scan(&event.IssueID, &event.ActorType, &event.ActorID, &event.From, &event.To, &event.At); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (h *Handler) loadPulseLoops(r *http.Request, workspaceID pgtype.UUID) ([]pulse.Loop, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT
			a.id::text,
			a.title,
			a.status,
			a.created_at,
			COALESCE((
				SELECT array_agg(DISTINCT trigger.kind ORDER BY trigger.kind)
				FROM autopilot_trigger trigger
				WHERE trigger.autopilot_id = a.id AND trigger.enabled
			), ARRAY[]::text[]),
			latest.triggered_at,
			latest.completed_at,
			COALESCE(latest.status, ''),
			failure.triggered_at
		FROM autopilot a
		LEFT JOIN LATERAL (
			SELECT run.triggered_at, run.completed_at, run.status
			FROM autopilot_run run
			WHERE run.autopilot_id = a.id
			ORDER BY run.triggered_at DESC, run.id DESC
			LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT run.triggered_at
			FROM autopilot_run run
			WHERE run.autopilot_id = a.id AND run.status IN ('failed', 'skipped')
			ORDER BY run.triggered_at DESC, run.id DESC
			LIMIT 1
		) failure ON true
		WHERE a.workspace_id = $1 AND a.status <> 'archived'
		ORDER BY a.title, a.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	loops := []pulse.Loop{}
	for rows.Next() {
		var loop pulse.Loop
		if err := rows.Scan(
			&loop.ID, &loop.Title, &loop.Status, &loop.CreatedAt, &loop.TriggerKinds,
			&loop.LastRunAt, &loop.LastCompletedAt, &loop.LastRunStatus, &loop.LastFailureAt,
		); err != nil {
			return nil, err
		}
		loops = append(loops, loop)
	}
	return loops, rows.Err()
}
