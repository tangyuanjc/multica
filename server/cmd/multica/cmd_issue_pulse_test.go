package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunIssuePulsePassesExplicitContractParameters(t *testing.T) {
	reviewerID := "24c7c069-a53d-491b-b4d0-342e258c6285"
	since := "2026-07-01T00:00:00Z"
	until := "2026-08-01T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues/pulse" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("reviewer_id"); got != reviewerID {
			t.Errorf("reviewer_id = %q, want %q", got, reviewerID)
		}
		if got := r.URL.Query().Get("since"); got != since {
			t.Errorf("since = %q, want %q", got, since)
		}
		if got := r.URL.Query().Get("until"); got != until {
			t.Errorf("until = %q, want %q", got, until)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": "issue-pulse.v1",
			"window":         map[string]any{"since": since, "until": until},
			"s1":             map[string]any{"overall": map[string]any{"sample_count": 3, "closed_count": 2, "open_count": 1, "p50_seconds": 60, "p90_seconds": 120}},
			"s2":             map[string]any{"first_pass": 1, "reworked": 1, "cancelled": 1, "in_flight": 1, "total": 4, "conservation_ok": true},
			"s3":             map[string]any{"done_total": 2, "untouched": 1, "ratio": 50},
			"s4":             map[string]any{"buckets": []any{}},
			"s5":             map[string]any{"loops": []any{}},
		})
	}))
	defer server.Close()

	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "bc2619a5-b59e-4777-ab3a-699f91d37fc8")
	t.Setenv("MULTICA_TOKEN", "mat_test_token")

	cmd := newIssuePulseTestCmd()
	_ = cmd.Flags().Set("reviewer-id", reviewerID)
	_ = cmd.Flags().Set("since", since)
	_ = cmd.Flags().Set("until", until)
	_ = cmd.Flags().Set("output", "json")

	oldStdout := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	err := runIssuePulse(cmd, nil)
	_ = writePipe.Close()
	os.Stdout = oldStdout
	output, _ := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("runIssuePulse: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	s2, _ := payload["s2"].(map[string]any)
	if s2["conservation_ok"] != true || s2["total"] != float64(4) {
		t.Fatalf("unexpected S2 output: %#v", s2)
	}
}

func TestRunIssuePulseRequiresReviewerID(t *testing.T) {
	cmd := newIssuePulseTestCmd()
	if err := runIssuePulse(cmd, nil); err == nil {
		t.Fatal("expected missing reviewer-id error")
	}
}

func newIssuePulseTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pulse"}
	cmd.Flags().String("reviewer-id", "", "")
	cmd.Flags().String("since", "", "")
	cmd.Flags().String("until", "", "")
	cmd.Flags().String("output", "json", "")
	return cmd
}
