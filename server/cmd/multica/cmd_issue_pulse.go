package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var issuePulseCmd = &cobra.Command{
	Use:   "pulse",
	Short: "Aggregate S1-S5 pulse metrics for autopilot-origin issues",
	RunE:  runIssuePulse,
}

func init() {
	issueCmd.AddCommand(issuePulseCmd)
	issuePulseCmd.Flags().String("reviewer-id", "", "Reviewer agent UUID used for independent acceptance decisions (required)")
	issuePulseCmd.Flags().String("since", "", "Cohort start as RFC3339 (default: 30 days before --until)")
	issuePulseCmd.Flags().String("until", "", "Cohort end as RFC3339 (default: now)")
	issuePulseCmd.Flags().String("output", "json", "Output format: json or table")
}

func runIssuePulse(cmd *cobra.Command, _ []string) error {
	reviewerID, _ := cmd.Flags().GetString("reviewer-id")
	reviewerID = strings.TrimSpace(reviewerID)
	if reviewerID == "" {
		return fmt.Errorf("--reviewer-id is required")
	}
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	if err := validatePulseTimeFlag("since", since); err != nil {
		return err
	}
	if err := validatePulseTimeFlag("until", until); err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	if output != "json" && output != "table" {
		return fmt.Errorf("invalid --output %q; valid values: json, table", output)
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	query := url.Values{"reviewer_id": []string{reviewerID}}
	if since != "" {
		query.Set("since", since)
	}
	if until != "" {
		query.Set("until", until)
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/issues/pulse?"+query.Encode(), &result); err != nil {
		return fmt.Errorf("get issue pulse: %w", err)
	}
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	printIssuePulseTable(result)
	return nil
}

func validatePulseTimeFlag(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("--%s must be RFC3339", name)
	}
	return nil
}

func printIssuePulseTable(result map[string]any) {
	window, _ := result["window"].(map[string]any)
	s1, _ := result["s1"].(map[string]any)
	overall, _ := s1["overall"].(map[string]any)
	s2, _ := result["s2"].(map[string]any)
	cli.PrintTable(os.Stdout,
		[]string{"WINDOW", "S1 P50", "S1 P90", "S1 OPEN/CLOSED", "S2 FIRST PASS", "S2 REWORKED", "S2 CANCELLED", "S2 IN FLIGHT", "S2 CONSERVED"},
		[][]string{{
			fmt.Sprintf("%s → %s", strVal(window, "since"), strVal(window, "until")),
			secondsLabel(overall["p50_seconds"]),
			secondsLabel(overall["p90_seconds"]),
			fmt.Sprintf("%s/%s", strVal(overall, "open_count"), strVal(overall, "closed_count")),
			strVal(s2, "first_pass"), strVal(s2, "reworked"), strVal(s2, "cancelled"), strVal(s2, "in_flight"), strVal(s2, "conservation_ok"),
		}},
	)

	s3, _ := result["s3"].(map[string]any)
	cli.PrintTable(os.Stdout,
		[]string{"S3 UNTOUCHED", "S3 DONE", "S3 RATIO"},
		[][]string{{strVal(s3, "untouched"), strVal(s3, "done_total"), percentLabel(s3["ratio"])}},
	)

	s4, _ := result["s4"].(map[string]any)
	cycleRows := [][]string{}
	for _, raw := range anySlice(s4["buckets"]) {
		row, _ := raw.(map[string]any)
		cycleRows = append(cycleRows, []string{strVal(row, "loop_title"), strVal(row, "done_count"), secondsLabel(row["p50_seconds"])})
	}
	cli.PrintTable(os.Stdout, []string{"S4 LOOP", "DONE", "P50"}, cycleRows)

	s5, _ := result["s5"].(map[string]any)
	loopRows := [][]string{}
	for _, raw := range anySlice(s5["loops"]) {
		row, _ := raw.(map[string]any)
		loopRows = append(loopRows, []string{
			strVal(row, "title"), strVal(row, "status"), strVal(row, "last_run_at"), strVal(row, "last_run_status"), strVal(row, "consecutive_healthy_days"),
		})
	}
	cli.PrintTable(os.Stdout, []string{"S5 LOOP", "STATUS", "LAST RUN", "RESULT", "HEALTHY DAYS"}, loopRows)
}

func secondsLabel(value any) string {
	numeric, ok := numberValue(value)
	if !ok {
		return "--"
	}
	return (time.Duration(numeric) * time.Second).String()
}

func percentLabel(value any) string {
	numeric, ok := numberValue(value)
	if !ok {
		return "--"
	}
	return strconv.FormatFloat(float64(numeric), 'f', 2, 64) + "%"
}

func numberValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}
