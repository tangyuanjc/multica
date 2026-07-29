package issueguard

import (
	"reflect"
	"testing"
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
