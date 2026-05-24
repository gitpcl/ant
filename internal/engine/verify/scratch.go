package verify

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// scratchTree is a throwaway copy of the scope root with a ProposedDiff applied
// to it, so an I/O verifier (compile, detector-clears) can run its check against
// the would-be post-fix state WITHOUT mutating the real working tree (the
// non-negotiable the approach gate records). The caller defers Cleanup.
//
// It is the shared machinery behind both I/O verifiers: copy the tree, apply the
// patches, run the check, throw it away. Keeping it in one place means the
// "never touch the real tree" guarantee is implemented and tested once.
type scratchTree struct {
	root string // absolute path to the scratch copy's root
}

// newScratchTree copies the scope root into a fresh temp dir and applies diff to
// the copy. It returns the scratch tree and a cleanup func. The real srcRoot is
// only READ; every write lands under the temp dir. A failure to copy or to apply
// a patch is returned to the caller, which turns it into a failing CheckResult
// (so a malformed diff is surfaced as a skip, never a panic).
func newScratchTree(srcRoot string, diff engine.ProposedDiff) (*scratchTree, func(), error) {
	if srcRoot == "" {
		srcRoot = "."
	}
	dst, err := os.MkdirTemp("", "ant-verify-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("verify: create scratch dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dst) }

	if err := copyTree(srcRoot, dst); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("verify: copy tree into scratch: %w", err)
	}

	st := &scratchTree{root: dst}
	if err := st.apply(diff); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return st, cleanup, nil
}

// apply writes each FileDiff's patched content into the scratch tree. Paths are
// resolved relative to the scratch root and confined to it (a patch path that
// escapes the root via .. is rejected — defense against a malicious/buggy diff).
func (s *scratchTree) apply(diff engine.ProposedDiff) error {
	for _, fd := range diff.Files {
		target, err := s.resolve(fd.Path)
		if err != nil {
			return err
		}
		original := readFileOrEmpty(target)
		patched, err := applyUnifiedPatch(original, fd.Patch)
		if err != nil {
			return fmt.Errorf("verify: apply patch to %s: %w", fd.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("verify: ensure dir for %s: %w", fd.Path, err)
		}
		if err := os.WriteFile(target, []byte(patched), 0o644); err != nil {
			return fmt.Errorf("verify: write patched %s: %w", fd.Path, err)
		}
	}
	return nil
}

// resolve maps a diff-relative path to an absolute path inside the scratch root,
// rejecting any path that would escape the root.
func (s *scratchTree) resolve(p string) (string, error) {
	clean := filepath.Clean("/" + filepath.ToSlash(p)) // anchor, strip leading ..
	abs := filepath.Join(s.root, filepath.FromSlash(clean))
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("verify: patch path %q escapes the scratch root", p)
	}
	return abs, nil
}

// readFileOrEmpty returns a file's content, or "" if it does not exist (a patch
// against a not-yet-existing file — e.g. a file-creating fix — starts from empty).
func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// copyTree recursively copies src into dst, preserving the relative layout. It
// skips the scratch-temp pattern itself defensively and follows no symlinks
// (a symlink is copied as a regular file of its target's content only if the
// walk yields it as a regular file; broken/dir symlinks are skipped). Hidden
// dirs like .git are copied so go build sees a faithful module.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, devices, sockets
		}
		return copyFile(path, target)
	})
}

// copyFile copies a single regular file's bytes from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// applyUnifiedPatch applies a unified-diff patch to the original file content and
// returns the patched content. It supports the constrained unified-diff the
// fixers emit (TECHSPEC §5.2 / fix/deterministic.go): standard `---`/`+++`
// headers, one or more `@@ -old,len +new,len @@` hunks, and `+`/`-`/` ` body
// lines. It validates context/removed lines against the original so a stale
// patch (one that no longer matches the file) is an error, not a silent corrupt
// write — exactly the kind of failure the compile/detector-clears gates exist to
// catch.
func applyUnifiedPatch(original, patch string) (string, error) {
	src := splitKeepLines(original)
	patchLines := splitPatchLines(patch)

	var out []string
	srcIdx := 0 // 0-based cursor into src

	i := 0
	for i < len(patchLines) {
		line := patchLines[i]
		switch {
		case hasPrefix(line, "--- "), hasPrefix(line, "+++ "):
			i++
			continue
		case hasPrefix(line, "@@"):
			h, err := parseHunkHeader(line)
			if err != nil {
				return "", err
			}
			// Copy unchanged lines from the cursor up to the hunk's old start.
			oldStart := h.oldStart - 1 // to 0-based
			if oldStart < 0 {
				oldStart = 0
			}
			if oldStart > len(src) {
				return "", fmt.Errorf("hunk start %d is past end of file (%d lines)", h.oldStart, len(src))
			}
			for srcIdx < oldStart {
				out = append(out, src[srcIdx])
				srcIdx++
			}
			i++
			// Consume the hunk body.
			for i < len(patchLines) && !hasPrefix(patchLines[i], "@@") {
				body := patchLines[i]
				switch {
				case hasPrefix(body, "+"):
					out = append(out, body[1:])
				case hasPrefix(body, "-"):
					if srcIdx >= len(src) {
						return "", fmt.Errorf("patch removes a line past end of file")
					}
					if src[srcIdx] != body[1:] {
						return "", fmt.Errorf("patch context mismatch at line %d: have %q, patch removes %q", srcIdx+1, src[srcIdx], body[1:])
					}
					srcIdx++
				case hasPrefix(body, " "):
					if srcIdx >= len(src) {
						return "", fmt.Errorf("patch context past end of file")
					}
					if src[srcIdx] != body[1:] {
						return "", fmt.Errorf("patch context mismatch at line %d: have %q, patch expects %q", srcIdx+1, src[srcIdx], body[1:])
					}
					out = append(out, src[srcIdx])
					srcIdx++
				default:
					// Unknown body line (blank or "\ No newline at end of file"): skip.
				}
				i++
			}
		default:
			i++ // ignore any preamble before the first header
		}
	}
	// Copy the remainder of the file after the last hunk.
	for srcIdx < len(src) {
		out = append(out, src[srcIdx])
		srcIdx++
	}
	return strings.Join(out, "\n"), nil
}

// hunkHeader is the parsed `@@ -oldStart,oldLen +newStart,newLen @@` line. Only
// the start positions are needed to drive the apply; lengths are validated
// implicitly by matching context/removed lines.
type hunkHeader struct {
	oldStart int
	newStart int
}

// parseHunkHeader parses `@@ -a,b +c,d @@` (the ,b/,d counts are optional and
// default to 1 per unified-diff convention).
func parseHunkHeader(line string) (hunkHeader, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || fields[0] != "@@" {
		return hunkHeader{}, fmt.Errorf("malformed hunk header %q", line)
	}
	oldStart, err := parseRangeStart(fields[1], '-')
	if err != nil {
		return hunkHeader{}, err
	}
	newStart, err := parseRangeStart(fields[2], '+')
	if err != nil {
		return hunkHeader{}, err
	}
	return hunkHeader{oldStart: oldStart, newStart: newStart}, nil
}

// parseRangeStart parses the start line from a `-a,b` or `+c,d` range token.
func parseRangeStart(token string, sign byte) (int, error) {
	if len(token) == 0 || token[0] != sign {
		return 0, fmt.Errorf("malformed range token %q", token)
	}
	body := token[1:]
	if comma := strings.IndexByte(body, ','); comma >= 0 {
		body = body[:comma]
	}
	n, err := strconv.Atoi(body)
	if err != nil {
		return 0, fmt.Errorf("malformed range start in %q: %w", token, err)
	}
	return n, nil
}

// splitKeepLines splits content into lines for patching, normalizing CRLF and
// dropping a single trailing empty element from a terminating newline so line
// indices align with editor/patch line numbers.
func splitKeepLines(content string) []string {
	if content == "" {
		return nil
	}
	s := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
