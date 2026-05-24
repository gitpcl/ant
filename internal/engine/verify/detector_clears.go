package verify

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gitpcl/ant/internal/engine"
)

// CheckDetectorClears is the canonical name of the detector-clears check.
const CheckDetectorClears = "detector-clears"

// detectorClearsVerifier re-runs the owning species' Detector against the
// post-fix tree and passes ONLY when the finding the fix targeted is gone
// (TECHSPEC §5.3). It is the verifier that proves a fix actually fixed the thing
// — a diff that compiles but leaves the smell in place must not be trusted.
//
// It applies the diff to a SCRATCH COPY (never the real tree) and runs the
// detector there. The detector is injected (engine.Detector) so the verifier is
// testable with a recorded/fake detector and needs no live ast-grep binary
// (TECHSPEC §12) — mirroring detect.NewRecorded.
type detectorClearsVerifier struct {
	detector engine.Detector
	finding  engine.Finding // the specific finding this fix targeted
}

// compile-time assertion that detectorClearsVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*detectorClearsVerifier)(nil)

// NewDetectorClears returns a detector-clears verifier bound to the owning
// detector and the specific finding the fix was meant to clear. The colony
// builds one per ant (the finding is per-ant); the detector is the same instance
// the species used to detect, re-run over the patched scratch tree.
func NewDetectorClears(detector engine.Detector, finding engine.Finding) engine.Verifier {
	return &detectorClearsVerifier{detector: detector, finding: finding}
}

// Verify applies the diff to a scratch copy, re-runs the detector over that
// copy, and reports whether the targeted finding still matches. Pass = the
// finding is gone. Fail (with a before/after detail) = it persists. A detector
// error or a scratch-prep failure is a failed check, never a panic, so the ant
// is skipped with a reason rather than crashing the colony.
func (v *detectorClearsVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	if v.detector == nil {
		return failResult(CheckDetectorClears, "no detector wired to re-run (cannot confirm the finding cleared)")
	}

	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckDetectorClears, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	// Re-run the detector over the patched scratch tree. Scope the run to the
	// scratch root so the detector reads the post-fix files, not the originals.
	scratchScope := scope
	scratchScope.Root = st.root
	scratchScope.Paths = rebasePaths(scope, st.root)

	after, err := v.detector.Detect(ctx, scratchScope)
	if err != nil {
		return failResult(CheckDetectorClears, fmt.Sprintf("re-running the detector failed: %v", err))
	}

	remaining := countMatching(after, v.finding)
	if remaining > 0 {
		return failResult(CheckDetectorClears, fmt.Sprintf(
			"the %q finding at %s:%d still matches after the fix (%d match(es) remain)",
			v.finding.Species, v.finding.File, v.finding.Span.StartLine, remaining))
	}
	return passResult(CheckDetectorClears, fmt.Sprintf(
		"the %q finding at %s:%d is cleared (detector reports 0 matches after the fix)",
		v.finding.Species, v.finding.File, v.finding.Span.StartLine))
}

// countMatching counts how many of the post-fix findings are "the same finding"
// as the one the fix targeted: same species and same file. Location can shift as
// lines are added/removed, so matching on species+file (not exact span) avoids a
// false pass when a fix merely moves the smell. If the species detector finds
// the same kind of issue in the same file after the fix, the fix did not clear
// it.
func countMatching(findings []engine.Finding, target engine.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Species == target.Species && f.File == target.File {
			n++
		}
	}
	return n
}

// rebasePaths translates the scope's explicit paths to live under the scratch
// root, so a path-scoped re-detect reads the patched copies. With no explicit
// paths, it returns nil (the detector falls back to the scratch root). A path
// given relative to the original scope root is preserved at the same relative
// position under the scratch root; an unrelatable path is joined directly.
func rebasePaths(scope engine.Scope, scratchRoot string) []string {
	if len(scope.Paths) == 0 {
		return nil
	}
	srcRoot := scope.Root
	if srcRoot == "" {
		srcRoot = "."
	}
	rebased := make([]string, 0, len(scope.Paths))
	for _, p := range scope.Paths {
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil || rel == "" {
			rel = p
		}
		rebased = append(rebased, filepath.Join(scratchRoot, rel))
	}
	return rebased
}
