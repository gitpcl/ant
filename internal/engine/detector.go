package engine

import "context"

// Detector locates findings. It never modifies code (TECHSPEC §5.1).
//
// Built-in adapters: astgrep (default), semgrep, eslint, command (script
// escape hatch). The manifest selects an implementation by named kind; each
// adapter asserts it satisfies this interface at compile time, e.g.:
//
//	var _ engine.Detector = (*astgrepDetector)(nil)
type Detector interface {
	Detect(ctx context.Context, scope Scope) ([]Finding, error)
}

// NamedDetector pairs a Detector with the species that owns it. Scout and the
// colony run loop carry detectors as NamedDetectors so they can honor --ant
// filtering and tag finding provenance by species without reaching into the
// species registry (TECHSPEC §6, §8).
type NamedDetector struct {
	Species  string
	Detector Detector
}
