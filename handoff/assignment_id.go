package handoff

import (
	"errors"
	"regexp"
	"unicode/utf8"
)

type assignmentIDMatcher struct {
	pattern  *regexp.Regexp
	maxBytes int
}

func assignmentIDs(config *AssignmentIDPolicy) (assignmentIDMatcher, error) {
	if config == nil {
		// Keep native validation and policy serialization unchanged for legacy
		// callers and their retained journal records.
		return assignmentIDMatcher{pattern: taskID}, nil
	}
	if config.MaxBytes < 1 || config.MaxBytes > 256 || len(config.Pattern) == 0 || len(config.Pattern) > 512 || !utf8.ValidString(config.Pattern) {
		return assignmentIDMatcher{}, errors.New("invalid assignment ID policy")
	}
	// Go regexp uses RE2 semantics. Bound the pattern before compilation and
	// enforce full-string matching even for alternations or multiline flags.
	pattern, err := regexp.Compile(`\A(?:` + config.Pattern + `)\z`)
	if err != nil {
		return assignmentIDMatcher{}, errors.New("invalid assignment ID policy")
	}
	return assignmentIDMatcher{pattern: pattern, maxBytes: config.MaxBytes}, nil
}

func (m assignmentIDMatcher) matches(id string) bool {
	if m.maxBytes != 0 {
		if len(id) == 0 || len(id) > m.maxBytes {
			return false
		}
		// The existing task-board query inserts a bare ID, never a string or
		// path. Configuration cannot widen this injection-safe alphabet.
		for i := 0; i < len(id); i++ {
			c := id[i]
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || (i > 0 && (c == '-' || c == '_')) {
				continue
			}
			return false
		}
	}
	return m.pattern.MatchString(id)
}
