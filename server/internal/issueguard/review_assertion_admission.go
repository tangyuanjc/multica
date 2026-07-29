package issueguard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var requiredHR37Fields = []string{"evidence_cmd", "threshold", "observed"}

// HR37EnforcementStart is the first instant at which new issues must satisfy
// the review assertion admission policy.
var HR37EnforcementStart = time.Date(2026, time.July, 27, 21, 28, 17, 0, time.UTC)

// ReviewAssertionAdmissionInput contains the issue fields used by the policy.
type ReviewAssertionAdmissionInput struct {
	Identifier  string
	Title       string
	Description string
	CreatedAt   time.Time
}

// ReviewAssertionAdmissionReason identifies the policy branch that produced a result.
type ReviewAssertionAdmissionReason string

const (
	ReviewAssertionAdmissionReasonAllowed               ReviewAssertionAdmissionReason = "allowed"
	ReviewAssertionAdmissionReasonGrandfathered         ReviewAssertionAdmissionReason = "grandfathered"
	ReviewAssertionAdmissionReasonExempt                ReviewAssertionAdmissionReason = "exempt"
	ReviewAssertionAdmissionReasonMissingAssertionBlock ReviewAssertionAdmissionReason = "missing_assertion_block"
	ReviewAssertionAdmissionReasonInvalidAssertionBlock ReviewAssertionAdmissionReason = "invalid_assertion_block"
	ReviewAssertionAdmissionReasonObservedRequired      ReviewAssertionAdmissionReason = "observed_required"
)

// ReviewAssertionAdmissionResult contains the policy verdict and its audit reason.
type ReviewAssertionAdmissionResult struct {
	Allowed bool
	Reason  ReviewAssertionAdmissionReason
	Message string
}

// CheckReviewAssertionAdmission evaluates an issue without executing evidence commands.
func CheckReviewAssertionAdmission(input ReviewAssertionAdmissionInput) ReviewAssertionAdmissionResult {
	if !input.CreatedAt.IsZero() && input.CreatedAt.Before(HR37EnforcementStart) {
		return ReviewAssertionAdmissionResult{
			Allowed: true,
			Reason:  ReviewAssertionAdmissionReasonGrandfathered,
			Message: "该 issue 创建于 HR37 强制执行时间之前。",
		}
	}

	if isReviewAssertionExemptTitle(input.Title) {
		return ReviewAssertionAdmissionResult{
			Allowed: true,
			Reason:  ReviewAssertionAdmissionReasonExempt,
			Message: "该 issue 符合固定的非工程豁免规则。",
		}
	}

	parsed := ParseHR37Assertions(input.Description)
	if !parsed.HasMarkers {
		return ReviewAssertionAdmissionResult{
			Reason: ReviewAssertionAdmissionReasonMissingAssertionBlock,
			Message: reviewAssertionRejectionMessage(
				input.Identifier,
				"缺少 HR37 断言块；请按 assert_N 内联映射格式补充 evidence_cmd、threshold 和 observed。",
			),
		}
	}
	if !parsed.Valid() {
		return ReviewAssertionAdmissionResult{
			Reason: ReviewAssertionAdmissionReasonInvalidAssertionBlock,
			Message: reviewAssertionRejectionMessage(
				input.Identifier,
				"HR37 断言块格式无效；请修正语法，确保每项都包含字符串类型的 evidence_cmd、threshold 和 observed。",
			),
		}
	}

	for _, assertion := range parsed.Assertions {
		if isBlankHR37Value(assertion.Observed) {
			return ReviewAssertionAdmissionResult{
				Reason: ReviewAssertionAdmissionReasonObservedRequired,
				Message: reviewAssertionRejectionMessage(
					input.Identifier,
					"HR37 断言块的 observed 不能为空；请执行证据命令后填写实际观测结果。",
				),
			}
		}
	}

	return ReviewAssertionAdmissionResult{
		Allowed: true,
		Reason:  ReviewAssertionAdmissionReasonAllowed,
		Message: "HR37 断言块完整。",
	}
}

func reviewAssertionRejectionMessage(identifier, message string) string {
	identifier = strings.TrimFunc(identifier, isHR37Whitespace)
	if identifier == "" {
		return message
	}
	return fmt.Sprintf("issue %q：%s", identifier, message)
}

func isReviewAssertionExemptTitle(title string) bool {
	title = strings.TrimLeftFunc(title, isHR37Whitespace)

	const knownUnbracketedPrefix = "🔍 Multica daily 扫描"
	if len(title) >= len(knownUnbracketedPrefix) &&
		strings.EqualFold(title[:len(knownUnbracketedPrefix)], knownUnbracketedPrefix) {
		return true
	}

	var (
		category string
		found    bool
	)
	switch {
	case strings.HasPrefix(title, "["):
		category, _, found = strings.Cut(title[1:], "]")
	case strings.HasPrefix(title, "【"):
		category, _, found = strings.Cut(title[len("【"):], "】")
	default:
		return false
	}
	if !found {
		return false
	}

	switch normalizeReviewAssertionExemptCategory(category) {
	case "daily",
		"work-daily",
		"日报",
		"工作日报",
		"日报状态票",
		"日报可见性",
		"loop radar daily",
		"天猫投放监控日报",
		"巡检",
		"日检",
		"周检",
		"patrol",
		"公告",
		"announcement":
		return true
	default:
		return false
	}
}

func normalizeReviewAssertionExemptCategory(category string) string {
	category = strings.TrimFunc(category, isHR37Whitespace)
	return strings.Map(func(value rune) rune {
		if value >= 'A' && value <= 'Z' {
			return value + ('a' - 'A')
		}
		return value
	}, category)
}

// HR37Assertion contains one parsed hr37 assertion block.
type HR37Assertion struct {
	Name            string
	EvidenceCommand string
	Threshold       string
	Observed        string
}

// HR37AssertionParseResult contains assertions and validation errors found in a description.
type HR37AssertionParseResult struct {
	Assertions []HR37Assertion
	HasMarkers bool
	Errors     []string
}

// Valid reports whether at least one assertion marker was present and every
// discovered assertion was valid.
func (r HR37AssertionParseResult) Valid() bool {
	return r.HasMarkers && len(r.Assertions) > 0 && len(r.Errors) == 0
}

// ParseHR37Assertions parses strict hr37 assertion mappings without executing
// their evidence commands.
func ParseHR37Assertions(description string) HR37AssertionParseResult {
	var result HR37AssertionParseResult
	seenMarkers := make(map[string]struct{})
	searchFrom := 0

	for searchFrom < len(description) {
		name, markerEnd, found := findNextHR37Marker(description, searchFrom)
		if !found {
			break
		}

		result.HasMarkers = true
		_, duplicate := seenMarkers[name]
		seenMarkers[name] = struct{}{}

		mappingStart := skipHR37Whitespace(description, markerEnd, len(description))
		if mappingStart == len(description) || description[mappingStart] != '{' {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: expected inline mapping", name))
			searchFrom = markerEnd
			continue
		}

		mappingEnd, balanced := findHR37MappingEnd(description, mappingStart)
		if !balanced {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: unterminated mapping", name))
			break
		}

		if duplicate {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: duplicate marker", name))
			searchFrom = mappingEnd
			continue
		}

		assertion, err := parseHR37Mapping(name, description[mappingStart:mappingEnd])
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.Assertions = append(result.Assertions, assertion)
		}
		searchFrom = mappingEnd
	}

	return result
}

func findNextHR37Marker(input string, start int) (string, int, bool) {
	const prefix = "assert_"

	for start < len(input) {
		offset := strings.Index(input[start:], prefix)
		if offset < 0 {
			return "", 0, false
		}

		markerStart := start + offset
		if markerStart > 0 && isHR37ASCIIWord(input[markerStart-1]) {
			start = markerStart + len(prefix)
			continue
		}

		digitsStart := markerStart + len(prefix)
		digitsEnd := digitsStart
		for digitsEnd < len(input) && input[digitsEnd] >= '0' && input[digitsEnd] <= '9' {
			digitsEnd++
		}
		if digitsEnd == digitsStart {
			start = digitsStart
			continue
		}

		colon := skipHR37Whitespace(input, digitsEnd, len(input))
		if colon < len(input) && input[colon] == ':' {
			return input[markerStart:digitsEnd], colon + 1, true
		}

		start = digitsStart
	}

	return "", 0, false
}

func isHR37ASCIIWord(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func findHR37MappingEnd(input string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(input); i++ {
		current := input[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch current {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch current {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}

	return len(input), false
}

func parseHR37Mapping(name, mapping string) (HR37Assertion, error) {
	fields := make(map[string]string, len(requiredHR37Fields))
	end := len(mapping) - 1
	position := skipHR37Whitespace(mapping, 1, end)

	for position < end {
		if !isHR37KeyStart(mapping[position]) {
			return HR37Assertion{}, fmt.Errorf("%s: expected unquoted ASCII key", name)
		}

		keyStart := position
		position++
		for position < end && isHR37KeyContinue(mapping[position]) {
			position++
		}
		key := mapping[keyStart:position]

		if !isRequiredHR37Field(key) {
			return HR37Assertion{}, fmt.Errorf("%s: unexpected key %q", name, key)
		}
		if _, exists := fields[key]; exists {
			return HR37Assertion{}, fmt.Errorf("%s: duplicate key %q", name, key)
		}

		position = skipHR37Whitespace(mapping, position, end)
		if position == end || mapping[position] != ':' {
			return HR37Assertion{}, fmt.Errorf("%s: expected colon after key %q", name, key)
		}
		position = skipHR37Whitespace(mapping, position+1, end)
		if position == end {
			return HR37Assertion{}, fmt.Errorf("%s: missing value for key %q", name, key)
		}

		var decodedValue any
		decoder := json.NewDecoder(strings.NewReader(mapping[position:end]))
		if err := decoder.Decode(&decodedValue); err != nil {
			return HR37Assertion{}, fmt.Errorf("%s: key %q must contain a valid JSON string", name, key)
		}
		value, ok := decodedValue.(string)
		if !ok {
			return HR37Assertion{}, fmt.Errorf("%s: key %q must contain a JSON string", name, key)
		}
		position += int(decoder.InputOffset())
		fields[key] = value

		position = skipHR37Whitespace(mapping, position, end)
		if position == end {
			break
		}
		if mapping[position] != ',' {
			return HR37Assertion{}, fmt.Errorf("%s: expected comma after key %q", name, key)
		}

		position = skipHR37Whitespace(mapping, position+1, end)
		if position == end {
			return HR37Assertion{}, fmt.Errorf("%s: trailing comma", name)
		}
	}

	for _, key := range requiredHR37Fields {
		if _, exists := fields[key]; !exists {
			return HR37Assertion{}, fmt.Errorf("%s: missing required key %q", name, key)
		}
	}
	if isBlankHR37Value(fields["evidence_cmd"]) {
		return HR37Assertion{}, fmt.Errorf("%s: evidence_cmd must not be blank", name)
	}
	if isBlankHR37Value(fields["threshold"]) {
		return HR37Assertion{}, fmt.Errorf("%s: threshold must not be blank", name)
	}

	return HR37Assertion{
		Name:            name,
		EvidenceCommand: fields["evidence_cmd"],
		Threshold:       fields["threshold"],
		Observed:        fields["observed"],
	}, nil
}

func skipHR37Whitespace(input string, start, end int) int {
	for start < end {
		value, size := utf8.DecodeRuneInString(input[start:end])
		if value == utf8.RuneError && size == 1 {
			return start
		}
		if !isHR37Whitespace(value) {
			return start
		}
		start += size
	}
	return start
}

func isHR37Whitespace(value rune) bool {
	return unicode.IsSpace(value) || value >= '\u001c' && value <= '\u001f'
}

func isBlankHR37Value(value string) bool {
	if value == "" {
		return true
	}
	for _, current := range value {
		if !isHR37Whitespace(current) {
			return false
		}
	}
	return true
}

func isHR37KeyStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func isHR37KeyContinue(value byte) bool {
	return isHR37KeyStart(value) || value >= '0' && value <= '9'
}

func isRequiredHR37Field(key string) bool {
	for _, required := range requiredHR37Fields {
		if key == required {
			return true
		}
	}
	return false
}
