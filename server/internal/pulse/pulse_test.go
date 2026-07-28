package pulse

import (
	"testing"
	"time"
)

func TestAggregateBuildsS1ToS5AndConservesCohort(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	reviewer := "24c7c069-a53d-491b-b4d0-342e258c6285"
	issues := []Issue{
		{ID: "first", LoopID: "loop-a", LoopTitle: "Daily", Status: "done", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
		{ID: "reworked", LoopID: "loop-a", LoopTitle: "Daily", Status: "done", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
		{ID: "cancelled", LoopID: "loop-a", LoopTitle: "Daily", Status: "cancelled", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
		{ID: "inflight", LoopID: "loop-a", LoopTitle: "Daily", Status: "in_review", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
		{ID: "reopened", LoopID: "loop-b", LoopTitle: "Weekly", Status: "done", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
		{ID: "membered", LoopID: "loop-b", LoopTitle: "Weekly", Status: "done", CreatorType: "agent", AssigneeType: "agent", CreatedAt: t0},
	}
	event := func(issueID, actorType, actorID, from, to string, hours int) StatusEvent {
		return StatusEvent{IssueID: issueID, ActorType: actorType, ActorID: actorID, From: from, To: to, At: t0.Add(time.Duration(hours) * time.Hour)}
	}
	events := []StatusEvent{
		event("first", "agent", "worker", "in_progress", "in_review", 2),
		event("first", "agent", reviewer, "in_review", "done", 4),
		event("reworked", "agent", "worker", "in_progress", "in_review", 1),
		event("reworked", "agent", reviewer, "in_review", "in_progress", 2),
		event("reworked", "agent", "worker", "in_progress", "in_review", 3),
		event("reworked", "agent", reviewer, "in_review", "done", 6),
		event("inflight", "agent", "worker", "in_progress", "in_review", 2),
		event("reopened", "agent", "worker", "in_progress", "in_review", 1),
		event("reopened", "agent", reviewer, "in_review", "done", 2),
		event("reopened", "system", "", "done", "todo", 3),
		event("reopened", "agent", "worker", "in_progress", "in_review", 4),
		event("reopened", "agent", reviewer, "in_review", "done", 5),
		event("membered", "agent", "worker", "in_progress", "in_review", 1),
		event("membered", "agent", reviewer, "in_review", "done", 2),
	}

	got := Aggregate(Input{
		ReviewerID:    reviewer,
		Since:         t0,
		Until:         t0.Add(30 * 24 * time.Hour),
		Issues:        issues,
		StatusEvents:  events,
		CommentActors: []CommentActor{{IssueID: "membered", ActorType: "member"}},
		Loops: []Loop{
			{ID: "loop-a", Title: "Daily", Status: "active", TriggerKinds: []string{"schedule"}, CreatedAt: t0.Add(-10 * 24 * time.Hour), LastRunAt: ptrTime(t0.Add(8 * time.Hour)), LastCompletedAt: ptrTime(t0.Add(9 * time.Hour)), LastRunStatus: "completed"},
			{ID: "loop-b", Title: "Weekly", Status: "paused", TriggerKinds: []string{"api"}, CreatedAt: t0.Add(-20 * 24 * time.Hour), LastRunAt: ptrTime(t0.Add(7 * time.Hour)), LastCompletedAt: ptrTime(t0.Add(7 * time.Hour)), LastFailureAt: ptrTime(t0.Add(6 * time.Hour)), LastRunStatus: "failed"},
		},
	})

	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", got.SchemaVersion, SchemaVersion)
	}
	if got.S2.FirstPass != 2 || got.S2.Reworked != 2 || got.S2.Cancelled != 1 || got.S2.InFlight != 1 || got.S2.Total != 6 {
		t.Fatalf("unexpected S2 buckets: %#v", got.S2)
	}
	if got.S2.FirstPass+got.S2.Reworked+got.S2.Cancelled+got.S2.InFlight != got.S2.Total || !got.S2.ConservationOK {
		t.Fatalf("S2 conservation failed: %#v", got.S2)
	}
	if got.S2.DoneReopened != 1 {
		t.Fatalf("done reopened = %d, want 1", got.S2.DoneReopened)
	}
	if got.S2.FirstPassRate == nil || *got.S2.FirstPassRate != 50 {
		t.Fatalf("first-pass rate = %v, want 50", got.S2.FirstPassRate)
	}
	if got.S1.Overall.SampleCount != 7 || got.S1.Overall.ClosedCount != 6 || got.S1.Overall.OpenCount != 1 {
		t.Fatalf("unexpected S1 counts: %#v", got.S1.Overall)
	}
	if got.S1.Overall.P50Seconds == nil || *got.S1.Overall.P50Seconds != 3600 {
		t.Fatalf("S1 p50 = %v, want 3600", got.S1.Overall.P50Seconds)
	}
	if got.S1.Overall.P90Seconds == nil || *got.S1.Overall.P90Seconds != 10800 {
		t.Fatalf("S1 p90 = %v, want 10800", got.S1.Overall.P90Seconds)
	}
	if got.S3.DoneTotal != 4 || got.S3.Untouched != 3 || got.S3.Ratio == nil || *got.S3.Ratio != 75 {
		t.Fatalf("unexpected S3: %#v", got.S3)
	}
	if len(got.S4.Buckets) != 2 || got.S4.Buckets[0].LoopID != "loop-a" || got.S4.Buckets[1].LoopID != "loop-b" {
		t.Fatalf("unexpected S4 buckets: %#v", got.S4.Buckets)
	}
	if len(got.S5.Loops) != 2 || got.S5.Loops[0].LastRunStatus != "completed" || got.S5.Loops[1].ConsecutiveHealthyDays != 0 {
		t.Fatalf("unexpected S5 loops: %#v", got.S5.Loops)
	}
}

func TestAggregateReturnsNilRatesForEmptyCohort(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	got := Aggregate(Input{ReviewerID: "reviewer", Since: t0, Until: t0.Add(time.Hour)})
	if !got.S2.ConservationOK || got.S2.Total != 0 || got.S2.FirstPassRate != nil || got.S3.Ratio != nil {
		t.Fatalf("unexpected empty response: %#v", got)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
