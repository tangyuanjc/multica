package issueguard

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var requiredHR37Fields = []string{"evidence_cmd", "threshold", "observed"}

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
