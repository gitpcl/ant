package engine

import "fmt"

// Severity ranks a finding's importance. It is an ordered enum: high > medium >
// low. The zero value is SeverityUnknown so an unset severity is detectable
// rather than silently defaulting to a real level.
type Severity int

const (
	// SeverityUnknown is the zero value: no severity has been assigned.
	SeverityUnknown Severity = iota
	// SeverityLow is the least important level.
	SeverityLow
	// SeverityMedium is the middle level.
	SeverityMedium
	// SeverityHigh is the most important level.
	SeverityHigh
)

// severityNames maps each defined Severity to its canonical lowercase token.
// It is the single source of truth for both String and ParseSeverity.
var severityNames = map[Severity]string{
	SeverityUnknown: "unknown",
	SeverityLow:     "low",
	SeverityMedium:  "medium",
	SeverityHigh:    "high",
}

// String returns the canonical lowercase token for a Severity. Unrecognized
// values render as "unknown" so logging an out-of-range Severity never panics.
func (s Severity) String() string {
	if name, ok := severityNames[s]; ok {
		return name
	}
	return "unknown"
}

// ParseSeverity converts a token ("low" | "medium" | "high") to a Severity.
// The match is exact and case-sensitive on the canonical tokens; any other
// value (including "unknown" and the empty string) is rejected with an error so
// callers validate severity at the boundary rather than trusting input.
func ParseSeverity(token string) (Severity, error) {
	switch token {
	case "low":
		return SeverityLow, nil
	case "medium":
		return SeverityMedium, nil
	case "high":
		return SeverityHigh, nil
	default:
		return SeverityUnknown, fmt.Errorf("engine: invalid severity %q (want low|medium|high)", token)
	}
}

// AtLeast reports whether s is ranked at or above the other severity. It backs
// the --fail-on CI gate (TECHSPEC §7.1): a finding meets the threshold when its
// severity is AtLeast the configured floor.
func (s Severity) AtLeast(other Severity) bool {
	return s >= other
}

// MarshalText implements encoding.TextMarshaler so a Severity serializes to its
// canonical token in JSON/TOML rather than to its integer rank. This keeps
// persisted Store state and the --json event stream human-readable and stable.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler, validating the token via
// ParseSeverity so deserialized severities go through the same boundary check.
func (s *Severity) UnmarshalText(text []byte) error {
	parsed, err := ParseSeverity(string(text))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
