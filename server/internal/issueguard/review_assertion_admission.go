package issueguard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	hr37AssertionMarkerPattern = regexp.MustCompile(`(^|[^A-Za-z0-9_])(assert_[0-9]+)[[:space:]]*:`)
	requiredHR37Fields         = []string{"evidence_cmd", "threshold", "observed"}
)

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
		match := hr37AssertionMarkerPattern.FindStringSubmatchIndex(description[searchFrom:])
		if match == nil {
			break
		}

		result.HasMarkers = true
		markerEnd := searchFrom + match[1]
		name := description[searchFrom+match[4] : searchFrom+match[5]]

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

		var value string
		decoder := json.NewDecoder(strings.NewReader(mapping[position:end]))
		if err := decoder.Decode(&value); err != nil {
			return HR37Assertion{}, fmt.Errorf("%s: key %q must contain a valid JSON string", name, key)
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
	if strings.TrimSpace(fields["evidence_cmd"]) == "" {
		return HR37Assertion{}, fmt.Errorf("%s: evidence_cmd must not be blank", name)
	}
	if strings.TrimSpace(fields["threshold"]) == "" {
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
		switch input[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			return start
		}
	}
	return start
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
