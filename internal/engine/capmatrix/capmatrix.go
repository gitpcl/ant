// Package capmatrix renders the built-in species capability matrix from the
// authoritative capability metadata (species.Resolved.Capabilities), so the
// docs/CAPABILITY-MATRIX.md table is GENERATED from one source instead of
// hand-maintained. A doc-consistency test (capmatrix_test.go) asserts the
// committed doc embeds exactly what Render produces, so the matrix can never
// silently drift from the manifests' capability fields (Sprint 022
// Future-Proofing #5).
//
// It lives in the engine layer (not cmd/ant) because it reads capabilities off
// resolved species — the same authority `ant doctor` and `ant species validate`
// read — keeping the CLI thin (TECHSPEC §3): the matrix is engine knowledge,
// the doc is its rendering.
package capmatrix

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/species"
)

// markerBegin/markerEnd delimit the generated table region inside the doc. The
// drift test replaces everything between them, so prose outside the markers is
// hand-authored and preserved while the table itself is always regenerated.
const (
	MarkerBegin = "<!-- BEGIN GENERATED CAPABILITY MATRIX -->"
	MarkerEnd   = "<!-- END GENERATED CAPABILITY MATRIX -->"
)

// Row is one species' capability row: its name plus the four resolved
// capability fields (requires_exec / requires_network / requires_tool /
// report_only) as read from species.Resolved.Capabilities().
type Row struct {
	Species         string
	RequiresExec    bool
	RequiresNetwork bool
	RequiresTool    string
	ReportOnly      bool
}

// BuiltinRows resolves every built-in species (the embedded tree, no on-disk
// user species, no ant.toml overrides) and projects each onto a capability Row,
// sorted by species name for a stable table. It is the single enumeration both
// Render and the drift test use, so the doc and the test see identical data.
func BuiltinRows() ([]Row, error) {
	resolver := species.NewResolver("", nil)
	resolved, err := resolver.Resolve(config.Config{})
	if err != nil {
		return nil, fmt.Errorf("capmatrix: resolve built-in species: %w", err)
	}
	rows := make([]Row, 0, len(resolved))
	for _, r := range resolved {
		c := r.Capabilities()
		rows = append(rows, Row{
			Species:         r.Manifest.Name,
			RequiresExec:    c.RequiresExec,
			RequiresNetwork: c.RequiresNetwork,
			RequiresTool:    c.RequiresTool,
			ReportOnly:      c.ReportOnly,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Species < rows[j].Species })
	return rows, nil
}

// Render builds the Markdown table for the rows. The column meanings map
// directly onto the capability metadata fields so a reader and the manifest
// speak the same vocabulary:
//
//   - ast-grep      → requires_tool == "ast-grep" (the AST matcher detector)
//   - command/script → requires_exec (a command detector or tool-runner fix execs)
//   - external tool  → requires_tool (the named binary the species needs on PATH)
//   - LLM            → requires_network (an llm fix calls a model endpoint)
//   - network        → requires_network
//   - report-only    → report_only (detects but proposes no fix)
//
// The "ast-grep" column is derived from requires_tool to answer the doc's
// headline question ("which species need ast-grep") without a separate field;
// the external-tool column shows the full requires_tool value. A "yes"/"-" cell
// keeps the table scannable. Render is pure (no I/O) so the test can compare its
// output byte-for-byte against the committed doc.
func Render(rows []Row) string {
	var b strings.Builder
	b.WriteString("| Species | ast-grep | command/script | external tool | LLM / network | report-only |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		astGrep := no
		if r.RequiresTool == species.DetectKindASTGrep {
			astGrep = yes
		}
		tool := r.RequiresTool
		if tool == "" {
			tool = "-"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n",
			r.Species, astGrep, yesNo(r.RequiresExec), tool, yesNo(r.RequiresNetwork), yesNo(r.ReportOnly))
	}
	return b.String()
}

// RenderBuiltins is the convenience entry point: resolve the built-ins and
// render their table in one call. The drift test and any future generator use
// it so they never re-implement the enumeration.
func RenderBuiltins() (string, error) {
	rows, err := BuiltinRows()
	if err != nil {
		return "", err
	}
	return Render(rows), nil
}

const (
	yes = "yes"
	no  = "-"
)

func yesNo(b bool) string {
	if b {
		return yes
	}
	return no
}
