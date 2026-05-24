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
