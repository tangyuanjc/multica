package issueguard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReviewAssertionAdmissionParser(t *testing.T) {
	tests := []struct {
		name           string
		description    string
		wantValid      bool
		wantHasMarkers bool
		wantCount      int
		wantErrors     bool
		wantAssertions []HR37Assertion
	}{
		{
			name:           "valid input",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}`,
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      1,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_1",
					EvidenceCommand: "go test ./...",
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
			},
		},
		{
			name:        "no marker",
			description: "Run the tests before review.",
		},
		{
			name:           "missing required key",
			description:    `assert_1: {evidence_cmd: "go test ./...", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "duplicate key",
			description:    `assert_1: {evidence_cmd: "go test ./...", evidence_cmd: "go test ./internal/...", threshold: "exit 0", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "unexpected key",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS", timeout: "30s"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "unterminated mapping",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name: "multiple assertions",
			description: `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}
assert_2: {evidence_cmd: "go vet ./...", threshold: "exit 0", observed: "PASS"}`,
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      2,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_1",
					EvidenceCommand: "go test ./...",
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
				{
					Name:            "assert_2",
					EvidenceCommand: "go vet ./...",
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
			},
		},
		{
			name:           "marker-like text inside evidence command is inert",
			description:    `assert_1: {evidence_cmd: "echo assert_2: inert", threshold: "exit 0", observed: "PASS"}`,
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      1,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_1",
					EvidenceCommand: "echo assert_2: inert",
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
			},
		},
		{
			name: "duplicate marker",
			description: `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}
assert_1: {evidence_cmd: "go vet ./...", threshold: "exit 0", observed: "PASS"}`,
			wantHasMarkers: true,
			wantCount:      1,
			wantErrors:     true,
		},
		{
			name:           "trailing comma",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS",}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "malformed JSON string",
			description:    `assert_1: {evidence_cmd: "go test \q", threshold: "exit 0", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "non-string value",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: 0, observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "blank evidence command",
			description:    `assert_1: {evidence_cmd: "   ", threshold: "exit 0", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "blank threshold",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: " ", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "blank observed allowed by parser",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: ""}`,
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      1,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_1",
					EvidenceCommand: "go test ./...",
					Threshold:       "exit 0",
				},
			},
		},
		{
			name:           "optional Unicode whitespace before colon and body",
			description:    "assert_7\u2003:\u00a0{evidence_cmd\u2009:\u2002\"go test ./...\", threshold: \"exit 0\", observed: \"PASS\"}",
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      1,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_7",
					EvidenceCommand: "go test ./...",
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
			},
		},
		{
			name:           "null observed is not a string",
			description:    `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: null}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
		{
			name:           "escaped quotes and braces remain inside command",
			description:    `assert_1: {evidence_cmd: "printf \"{still data}\"", threshold: "exit 0", observed: "PASS"}`,
			wantValid:      true,
			wantHasMarkers: true,
			wantCount:      1,
			wantAssertions: []HR37Assertion{
				{
					Name:            "assert_1",
					EvidenceCommand: `printf "{still data}"`,
					Threshold:       "exit 0",
					Observed:        "PASS",
				},
			},
		},
		{
			name:           "Python-only whitespace is blank",
			description:    `assert_1: {evidence_cmd: "\u001c", threshold: "exit 0", observed: "PASS"}`,
			wantHasMarkers: true,
			wantErrors:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseHR37Assertions(tt.description)

			if got.Valid() != tt.wantValid {
				t.Errorf("Valid() = %v, want %v; result = %#v", got.Valid(), tt.wantValid, got)
			}
			if got.HasMarkers != tt.wantHasMarkers {
				t.Errorf("HasMarkers = %v, want %v", got.HasMarkers, tt.wantHasMarkers)
			}
			if len(got.Assertions) != tt.wantCount {
				t.Errorf("len(Assertions) = %d, want %d; assertions = %#v", len(got.Assertions), tt.wantCount, got.Assertions)
			}
			if (len(got.Errors) > 0) != tt.wantErrors {
				t.Errorf("Errors = %#v, wantErrors = %v", got.Errors, tt.wantErrors)
			}
			if tt.wantAssertions != nil && !reflect.DeepEqual(got.Assertions, tt.wantAssertions) {
				t.Errorf("Assertions = %#v, want %#v", got.Assertions, tt.wantAssertions)
			}
		})
	}
}

func TestReviewAssertionAdmissionParserTreatsEvidenceCommandAsData(t *testing.T) {
	sentinelPath := filepath.Join(t.TempDir(), "evidence-command-was-executed")
	evidenceCommand := "touch " + sentinelPath

	encodedCommand, err := json.Marshal(evidenceCommand)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	description := `assert_1: {evidence_cmd: ` + string(encodedCommand) + `, threshold: "exit 0", observed: "PASS"}`

	got := ParseHR37Assertions(description)

	if !got.HasMarkers || !got.Valid() {
		t.Fatalf("ParseHR37Assertions() = %#v, want valid assertion", got)
	}
	if len(got.Assertions) != 1 {
		t.Fatalf("len(Assertions) = %d, want 1", len(got.Assertions))
	}
	if got.Assertions[0].EvidenceCommand != evidenceCommand {
		t.Errorf("EvidenceCommand = %q, want %q", got.Assertions[0].EvidenceCommand, evidenceCommand)
	}
	if _, err := os.Stat(sentinelPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sentinel path was created or could not be checked: %v", err)
	}
}

func TestReviewAssertionAdmissionPolicy(t *testing.T) {
	const (
		sentinelEvidenceCommand  = "printf DO_NOT_LEAK_HR37_EVIDENCE"
		validDescription         = `assert_1: {evidence_cmd: "go test ./...", threshold: "exit 0", observed: "PASS"}`
		missingDescription       = "Run " + sentinelEvidenceCommand + " before review."
		invalidDescription       = `assert_1: {evidence_cmd: "` + sentinelEvidenceCommand + `", threshold: "exit 0", observed: "PASS",}`
		blankObservedDescription = `assert_1: {evidence_cmd: "` + sentinelEvidenceCommand + `", threshold: "exit 0", observed: "\u001c\u2003"}`

		reasonAllowed               ReviewAssertionAdmissionReason = "allowed"
		reasonGrandfathered         ReviewAssertionAdmissionReason = "grandfathered"
		reasonExempt                ReviewAssertionAdmissionReason = "exempt"
		reasonMissingAssertionBlock ReviewAssertionAdmissionReason = "missing_assertion_block"
		reasonInvalidAssertionBlock ReviewAssertionAdmissionReason = "invalid_assertion_block"
		reasonObservedRequired      ReviewAssertionAdmissionReason = "observed_required"
	)

	inputAt := func(title, description string, createdAt time.Time) ReviewAssertionAdmissionInput {
		return ReviewAssertionAdmissionInput{
			Identifier:  "WS-2597",
			Title:       title,
			Description: description,
			CreatedAt:   createdAt,
		}
	}

	tests := []struct {
		name        string
		input       ReviewAssertionAdmissionInput
		wantAllowed bool
		wantReason  ReviewAssertionAdmissionReason
	}{
		{
			name:       "missing assertion block",
			input:      inputAt("[P1] Add admission policy", missingDescription, HR37EnforcementStart.Add(time.Nanosecond)),
			wantReason: reasonMissingAssertionBlock,
		},
		{
			name:        "valid completed assertion",
			input:       inputAt("[P1] Add admission policy", validDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonAllowed,
		},
		{
			name:       "blank observed under Python whitespace semantics",
			input:      inputAt("[P1] Add admission policy", blankObservedDescription, HR37EnforcementStart),
			wantReason: reasonObservedRequired,
		},
		{
			name:       "invalid assertion syntax",
			input:      inputAt("[P1] Add admission policy", invalidDescription, HR37EnforcementStart),
			wantReason: reasonInvalidAssertionBlock,
		},
		{
			name:        "legacy issue before cutoff is grandfathered",
			input:       inputAt("[P1] Legacy issue", missingDescription, HR37EnforcementStart.Add(-time.Nanosecond)),
			wantAllowed: true,
			wantReason:  reasonGrandfathered,
		},
		{
			name:       "exact cutoff is enforced",
			input:      inputAt("[P1] Exact cutoff", missingDescription, HR37EnforcementStart),
			wantReason: reasonMissingAssertionBlock,
		},
		{
			name:       "zero creation time is enforced",
			input:      inputAt("[P1] New issue", missingDescription, time.Time{}),
			wantReason: reasonMissingAssertionBlock,
		},
		{
			name:        "daily category is exempt",
			input:       inputAt("[daily] Status report", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "work daily category is exempt",
			input:       inputAt("[work-daily] Status report", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "Chinese daily category with leading whitespace is exempt",
			input:       inputAt("\u2003【日报】状态报告", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "inspection category is exempt",
			input:       inputAt("【巡检】服务检查", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "daily inspection category is exempt",
			input:       inputAt("【日检】服务检查", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "weekly inspection category is exempt",
			input:       inputAt("[周检] 服务检查", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "patrol category is ASCII case insensitive",
			input:       inputAt("[PaTrOl] Service check", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "Chinese announcement category is exempt",
			input:       inputAt("【公告】维护通知", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "announcement category is ASCII case insensitive",
			input:       inputAt("[AnNoUnCeMeNt] Maintenance", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "Loop Radar Daily category is exempt",
			input:       inputAt("[Loop Radar Daily] Scan", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:        "known unbracketed prefix is exempt and ASCII case insensitive",
			input:       inputAt("  🔍 mUlTiCa DaIlY 扫描：workspace", missingDescription, HR37EnforcementStart),
			wantAllowed: true,
			wantReason:  reasonExempt,
		},
		{
			name:       "daily marker in later prose does not exempt engineering work",
			input:      inputAt("[P1] 修复日报生成器", missingDescription, HR37EnforcementStart),
			wantReason: reasonMissingAssertionBlock,
		},
		{
			name:       "misleading second bracket does not exempt engineering work",
			input:      inputAt("[P1][日报] 修复生成器", missingDescription, HR37EnforcementStart),
			wantReason: reasonMissingAssertionBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckReviewAssertionAdmission(tt.input)

			if got.Allowed != tt.wantAllowed {
				t.Errorf("Allowed = %v, want %v; result = %#v", got.Allowed, tt.wantAllowed, got)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q; result = %#v", got.Reason, tt.wantReason, got)
			}
			if got.Message == "" {
				t.Error("Message is empty")
			}
			if !got.Allowed {
				if !strings.Contains(got.Message, "断言块") {
					t.Errorf("rejection Message = %q, want 断言块", got.Message)
				}
				if !strings.Contains(got.Message, "请") {
					t.Errorf("rejection Message = %q, want remediation hint", got.Message)
				}
				if strings.Contains(got.Message, sentinelEvidenceCommand) {
					t.Errorf("rejection Message leaked evidence command: %q", got.Message)
				}
			}
		})
	}

	t.Run("evidence commands remain inert", func(t *testing.T) {
		sentinelPath := filepath.Join(t.TempDir(), "admission-executed-command")
		evidenceCommand := "touch " + sentinelPath
		encodedCommand, err := json.Marshal(evidenceCommand)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		got := CheckReviewAssertionAdmission(inputAt(
			"[P1] Verify inert admission",
			`assert_1: {evidence_cmd: `+string(encodedCommand)+`, threshold: "exit 0", observed: "PASS"}`,
			HR37EnforcementStart,
		))

		if !got.Allowed || got.Reason != reasonAllowed {
			t.Fatalf("CheckReviewAssertionAdmission() = %#v, want allowed", got)
		}
		if _, err := os.Stat(sentinelPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sentinel path was created or could not be checked: %v", err)
		}
	})
}
