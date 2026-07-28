// Package pulse computes the read-only S1-S5 organization pulse projection.
package pulse

import (
	"math"
	"sort"
	"time"
)

const SchemaVersion = "issue-pulse.v1"

type Issue struct {
	ID           string
	LoopID       string
	LoopTitle    string
	Status       string
	CreatorType  string
	AssigneeType string
	CreatedAt    time.Time
	RerunCount   int
	HumanTouched bool
}

type StatusEvent struct {
	IssueID   string
	ActorType string
	ActorID   string
	From      string
	To        string
	At        time.Time
}

type CommentActor struct {
	IssueID   string
	ActorType string
}

type Loop struct {
	ID              string
	Title           string
	Status          string
	TriggerKinds    []string
	CreatedAt       time.Time
	LastRunAt       *time.Time
	LastCompletedAt *time.Time
	LastFailureAt   *time.Time
	LastRunStatus   string
}

type Input struct {
	ReviewerID    string
	Since         time.Time
	Until         time.Time
	Issues        []Issue
	StatusEvents  []StatusEvent
	CommentActors []CommentActor
	Loops         []Loop
}

type Window struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

type ReviewBucket struct {
	LoopID      string `json:"loop_id,omitempty"`
	LoopTitle   string `json:"loop_title,omitempty"`
	SampleCount int    `json:"sample_count"`
	ClosedCount int    `json:"closed_count"`
	OpenCount   int    `json:"open_count"`
	P50Seconds  *int64 `json:"p50_seconds"`
	P90Seconds  *int64 `json:"p90_seconds"`
}

type S1 struct {
	Overall ReviewBucket   `json:"overall"`
	Buckets []ReviewBucket `json:"buckets"`
}

type ConservationBucket struct {
	LoopID         string   `json:"loop_id,omitempty"`
	LoopTitle      string   `json:"loop_title,omitempty"`
	FirstPass      int      `json:"first_pass"`
	Reworked       int      `json:"reworked"`
	Cancelled      int      `json:"cancelled"`
	InFlight       int      `json:"in_flight"`
	Total          int      `json:"total"`
	DoneReopened   int      `json:"done_reopened"`
	FirstPassRate  *float64 `json:"first_pass_rate"`
	ConservationOK bool     `json:"conservation_ok"`
}

type S2 struct {
	ConservationBucket
	Buckets []ConservationBucket `json:"buckets"`
}

type TouchBucket struct {
	LoopID    string   `json:"loop_id,omitempty"`
	LoopTitle string   `json:"loop_title,omitempty"`
	DoneTotal int      `json:"done_total"`
	Untouched int      `json:"untouched"`
	Ratio     *float64 `json:"ratio"`
}

type S3 struct {
	TouchBucket
	Buckets []TouchBucket `json:"buckets"`
}

type CycleBucket struct {
	LoopID     string `json:"loop_id"`
	LoopTitle  string `json:"loop_title"`
	DoneCount  int    `json:"done_count"`
	P50Seconds *int64 `json:"p50_seconds"`
}

type S4 struct {
	Buckets []CycleBucket `json:"buckets"`
}

type LoopView struct {
	ID                     string   `json:"id"`
	Title                  string   `json:"title"`
	Status                 string   `json:"status"`
	TriggerKinds           []string `json:"trigger_kinds"`
	LastRunAt              *string  `json:"last_run_at"`
	LastResultAt           *string  `json:"last_result_at"`
	LastRunStatus          string   `json:"last_run_status"`
	ConsecutiveHealthyDays int      `json:"consecutive_healthy_days"`
}

type S5 struct {
	Loops []LoopView `json:"loops"`
}

type Response struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	ReviewerID    string `json:"reviewer_id"`
	Window        Window `json:"window"`
	Cohort        struct {
		Scope string `json:"scope"`
		Total int    `json:"total"`
	} `json:"cohort"`
	S1 S1 `json:"s1"`
	S2 S2 `json:"s2"`
	S3 S3 `json:"s3"`
	S4 S4 `json:"s4"`
	S5 S5 `json:"s5"`
}

type issueFacts struct {
	reviewDurations         []int64
	reviewSamples           int
	openReviews             int
	reviewerDone            []time.Time
	firstDecision           *StatusEvent
	firstDecisionByReviewer bool
	doneReopened            bool
	humanTouched            bool
}

// Aggregate builds the versioned public response from already tenant-scoped rows.
func Aggregate(input Input) Response {
	response := Response{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   input.Until.UTC().Format(time.RFC3339Nano),
		ReviewerID:    input.ReviewerID,
		Window: Window{
			Since: input.Since.UTC().Format(time.RFC3339Nano),
			Until: input.Until.UTC().Format(time.RFC3339Nano),
		},
		S1: S1{Buckets: []ReviewBucket{}},
		S2: S2{Buckets: []ConservationBucket{}},
		S3: S3{Buckets: []TouchBucket{}},
		S4: S4{Buckets: []CycleBucket{}},
		S5: S5{Loops: []LoopView{}},
	}
	response.Cohort.Scope = "autopilot_origin"
	response.Cohort.Total = len(input.Issues)

	commentsByIssue := make(map[string]bool)
	for _, comment := range input.CommentActors {
		if comment.ActorType == "member" {
			commentsByIssue[comment.IssueID] = true
		}
	}
	eventsByIssue := make(map[string][]StatusEvent)
	for _, event := range input.StatusEvents {
		eventsByIssue[event.IssueID] = append(eventsByIssue[event.IssueID], event)
	}
	for issueID := range eventsByIssue {
		sort.SliceStable(eventsByIssue[issueID], func(i, j int) bool {
			return eventsByIssue[issueID][i].At.Before(eventsByIssue[issueID][j].At)
		})
	}

	type groupFacts struct {
		title           string
		review          ReviewBucket
		reviewDurations []int64
		s2              ConservationBucket
		touch           TouchBucket
		cycles          []int64
	}
	groups := make(map[string]*groupFacts)
	overallReviewDurations := []int64{}

	for _, issue := range input.Issues {
		group := groups[issue.LoopID]
		if group == nil {
			group = &groupFacts{title: issue.LoopTitle}
			groups[issue.LoopID] = group
		}
		facts := deriveIssueFacts(issue, eventsByIssue[issue.ID], commentsByIssue[issue.ID], input.ReviewerID)
		group.review.SampleCount += facts.reviewSamples
		group.review.ClosedCount += len(facts.reviewDurations)
		group.review.OpenCount += facts.openReviews
		group.reviewDurations = append(group.reviewDurations, facts.reviewDurations...)
		overallReviewDurations = append(overallReviewDurations, facts.reviewDurations...)
		response.S1.Overall.SampleCount += facts.reviewSamples
		response.S1.Overall.ClosedCount += len(facts.reviewDurations)
		response.S1.Overall.OpenCount += facts.openReviews

		classify(&response.S2.ConservationBucket, issue, facts)
		classify(&group.s2, issue, facts)

		if issue.Status == "done" {
			response.S3.DoneTotal++
			group.touch.DoneTotal++
			if issue.CreatorType != "member" && issue.AssigneeType != "member" && !facts.humanTouched && len(facts.reviewerDone) > 0 {
				response.S3.Untouched++
				group.touch.Untouched++
			}
			if len(facts.reviewerDone) > 0 {
				doneAt := facts.reviewerDone[len(facts.reviewerDone)-1]
				if !doneAt.Before(issue.CreatedAt) {
					seconds := int64(doneAt.Sub(issue.CreatedAt).Seconds())
					group.cycles = append(group.cycles, seconds)
				}
			}
		}
	}

	response.S1.Overall.P50Seconds = percentile(overallReviewDurations, 0.50)
	response.S1.Overall.P90Seconds = percentile(overallReviewDurations, 0.90)
	finalizeConservation(&response.S2.ConservationBucket)
	response.S3.Ratio = ratio(response.S3.Untouched, response.S3.DoneTotal)

	groupIDs := make([]string, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Slice(groupIDs, func(i, j int) bool {
		left, right := groups[groupIDs[i]], groups[groupIDs[j]]
		if left.title == right.title {
			return groupIDs[i] < groupIDs[j]
		}
		return left.title < right.title
	})
	for _, id := range groupIDs {
		group := groups[id]
		group.review.LoopID, group.review.LoopTitle = id, group.title
		group.review.P50Seconds = percentile(group.reviewDurations, 0.50)
		group.review.P90Seconds = percentile(group.reviewDurations, 0.90)
		response.S1.Buckets = append(response.S1.Buckets, group.review)

		group.s2.LoopID, group.s2.LoopTitle = id, group.title
		finalizeConservation(&group.s2)
		response.S2.Buckets = append(response.S2.Buckets, group.s2)

		group.touch.LoopID, group.touch.LoopTitle = id, group.title
		group.touch.Ratio = ratio(group.touch.Untouched, group.touch.DoneTotal)
		response.S3.Buckets = append(response.S3.Buckets, group.touch)
		response.S4.Buckets = append(response.S4.Buckets, CycleBucket{
			LoopID: id, LoopTitle: group.title, DoneCount: len(group.cycles), P50Seconds: percentile(group.cycles, 0.50),
		})
	}

	response.S5.Loops = buildLoopViews(input.Loops, input.Until)
	return response
}

func deriveIssueFacts(issue Issue, events []StatusEvent, memberComment bool, reviewerID string) issueFacts {
	facts := issueFacts{humanTouched: issue.HumanTouched || memberComment}
	var reviewStarted *time.Time
	for index := range events {
		event := events[index]
		if event.ActorType == "member" {
			facts.humanTouched = true
		}
		if event.From == "done" && event.To != "done" {
			facts.doneReopened = true
		}
		if event.To == "in_review" && event.From != "in_review" && reviewStarted == nil {
			started := event.At
			reviewStarted = &started
			facts.reviewSamples++
		}
		if event.From != "in_review" || event.To == "in_review" {
			continue
		}
		if facts.firstDecision == nil {
			decision := event
			facts.firstDecision = &decision
			facts.firstDecisionByReviewer = event.ActorType == "agent" && event.ActorID == reviewerID
		}
		if event.ActorType == "agent" && event.ActorID == reviewerID && event.To == "done" {
			facts.reviewerDone = append(facts.reviewerDone, event.At)
		}
		if reviewStarted != nil && !event.At.Before(*reviewStarted) {
			facts.reviewDurations = append(facts.reviewDurations, int64(event.At.Sub(*reviewStarted).Seconds()))
			reviewStarted = nil
		}
	}
	if reviewStarted != nil {
		facts.openReviews = 1
	}
	return facts
}

func classify(bucket *ConservationBucket, issue Issue, facts issueFacts) {
	bucket.Total++
	if facts.doneReopened {
		bucket.DoneReopened++
	}
	switch issue.Status {
	case "cancelled":
		bucket.Cancelled++
	case "done":
		firstPass := facts.firstDecision != nil &&
			facts.firstDecisionByReviewer &&
			facts.firstDecision.To == "done" &&
			len(facts.reviewerDone) == 1 &&
			!facts.doneReopened && issue.RerunCount == 0
		if firstPass {
			bucket.FirstPass++
		} else {
			bucket.Reworked++
		}
	default:
		bucket.InFlight++
	}
}

func finalizeConservation(bucket *ConservationBucket) {
	bucket.ConservationOK = bucket.FirstPass+bucket.Reworked+bucket.Cancelled+bucket.InFlight == bucket.Total
	doneTotal := bucket.FirstPass + bucket.Reworked
	bucket.FirstPassRate = ratio(bucket.FirstPass, doneTotal)
}

func ratio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) * 100 / float64(denominator)
	value = math.Round(value*100) / 100
	return &value
}

func percentile(values []int64, fraction float64) *int64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(fraction*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	value := sorted[index]
	return &value
}

func buildLoopViews(loops []Loop, until time.Time) []LoopView {
	views := make([]LoopView, 0, len(loops))
	for _, loop := range loops {
		triggerKinds := append([]string(nil), loop.TriggerKinds...)
		sort.Strings(triggerKinds)
		view := LoopView{
			ID: loop.ID, Title: loop.Title, Status: loop.Status, TriggerKinds: triggerKinds,
			LastRunAt: timeString(loop.LastRunAt), LastResultAt: timeString(loop.LastCompletedAt), LastRunStatus: loop.LastRunStatus,
		}
		if loop.LastRunStatus == "completed" {
			healthySince := loop.CreatedAt
			if loop.LastFailureAt != nil && loop.LastFailureAt.After(healthySince) {
				healthySince = *loop.LastFailureAt
			}
			if until.After(healthySince) {
				view.ConsecutiveHealthyDays = int(until.Sub(healthySince) / (24 * time.Hour))
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Title == views[j].Title {
			return views[i].ID < views[j].ID
		}
		return views[i].Title < views[j].Title
	})
	return views
}

func timeString(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}
