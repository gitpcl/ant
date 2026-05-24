package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		want    Severity
		wantErr bool
	}{
		{name: "low", token: "low", want: SeverityLow},
		{name: "medium", token: "medium", want: SeverityMedium},
		{name: "high", token: "high", want: SeverityHigh},
		{name: "unknown token rejected", token: "unknown", wantErr: true},
		{name: "empty rejected", token: "", wantErr: true},
		{name: "uppercase rejected", token: "HIGH", wantErr: true},
		{name: "garbage rejected", token: "critical", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSeverity(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSeverity(%q): want error, got nil (value %v)", tc.token, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSeverity(%q): unexpected error %v", tc.token, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSeverity(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func TestSeverityString(t *testing.T) {
	tests := []struct {
		sev  Severity
		want string
	}{
		{SeverityUnknown, "unknown"},
		{SeverityLow, "low"},
		{SeverityMedium, "medium"},
		{SeverityHigh, "high"},
		{Severity(99), "unknown"}, // out-of-range never panics
	}
	for _, tc := range tests {
		if got := tc.sev.String(); got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tc.sev, got, tc.want)
		}
	}
}

func TestSeverityOrdering(t *testing.T) {
	// high > medium > low — the ordering the --fail-on gate depends on.
	if !(SeverityHigh > SeverityMedium) {
		t.Errorf("expected high > medium")
	}
	if !(SeverityMedium > SeverityLow) {
		t.Errorf("expected medium > low")
	}
	if !(SeverityLow > SeverityUnknown) {
		t.Errorf("expected low > unknown (zero value sorts below all real levels)")
	}

	atLeast := []struct {
		s, floor Severity
		want     bool
	}{
		{SeverityHigh, SeverityHigh, true},
		{SeverityHigh, SeverityMedium, true},
		{SeverityMedium, SeverityHigh, false},
		{SeverityLow, SeverityLow, true},
		{SeverityLow, SeverityMedium, false},
	}
	for _, tc := range atLeast {
		if got := tc.s.AtLeast(tc.floor); got != tc.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", tc.s, tc.floor, got, tc.want)
		}
	}
}

func TestSeverityZeroValue(t *testing.T) {
	var s Severity
	if s != SeverityUnknown {
		t.Errorf("zero-value Severity = %v, want SeverityUnknown", s)
	}
	if s.String() != "unknown" {
		t.Errorf("zero-value String() = %q, want %q", s.String(), "unknown")
	}
}

func TestSeverityTextRoundTrip(t *testing.T) {
	for _, sev := range []Severity{SeverityLow, SeverityMedium, SeverityHigh} {
		data, err := json.Marshal(sev)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", sev, err)
		}
		var got Severity
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if got != sev {
			t.Errorf("round-trip %v -> %s -> %v", sev, data, got)
		}
	}

	// An invalid token must fail unmarshalling (boundary validation).
	var bad Severity
	if err := json.Unmarshal([]byte(`"critical"`), &bad); err == nil {
		t.Errorf("Unmarshal of invalid severity token should error")
	}
}

func TestRunNotFoundErrorIs(t *testing.T) {
	err := error(&RunNotFoundError{ID: "abc"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Errorf("RunNotFoundError should satisfy errors.Is(ErrRunNotFound)")
	}
	if msg := err.Error(); !strings.Contains(msg, "abc") {
		t.Errorf("RunNotFoundError.Error() = %q, want it to name the run id", msg)
	}
}
