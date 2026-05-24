package detect

import "github.com/gitpcl/ant/internal/engine"

// builtinSpecies enumerates the v1 deterministic-detection species whose
// ast-grep rule files ship embedded with the binary (TECHSPEC §4, M1/M2). Each
// entry names the species and its rule file; the species manifest system (a
// later sprint) will supersede this static table, but it gives scout a concrete
// zero-config detector set today without depending on the unbuilt registry.
var builtinSpecies = []struct {
	Name string
	Rule string
}{
	{Name: "dead-code", Rule: "dead-code/detect.yml"},
	{Name: "unused-import", Rule: "unused-import/detect.yml"},
}

// Builtins returns the default scout detector set: one ast-grep detector per
// built-in species, paired with the owning species name for --ant filtering and
// provenance. The CLI composition root calls this for a zero-config run. Each
// detector shells out to ast-grep at Detect time; a missing ast-grep binary
// surfaces as a typed *engine.DetectorUnavailableError (exit code 2), never a
// crash.
//
// rulesRoot is prepended to each species' rule path so the caller controls
// where rule files resolve from (the embedded species tree, once it lands).
func Builtins(rulesRoot string) []engine.NamedDetector {
	out := make([]engine.NamedDetector, 0, len(builtinSpecies))
	for _, s := range builtinSpecies {
		rule := s.Rule
		if rulesRoot != "" {
			rule = rulesRoot + "/" + s.Rule
		}
		out = append(out, engine.NamedDetector{
			Species:  s.Name,
			Detector: NewASTGrep(s.Name, rule),
		})
	}
	return out
}
