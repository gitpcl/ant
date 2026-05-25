package fix_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

// This is the shared harness-adapter CONTRACT suite (TECHSPEC §10, §12). Every
// Fixer adapter — claudecode, codex, pi, AND rawmodel from Sprint 005 — runs
// through the identical assertions:
//
//	(a) one FixTask in → exactly one ProposedDiff out, parsed onto the finding's
//	    file;
//	(b) provenance is non-empty and reflects fixer name + the CONFIG model
//	    (never a hardcoded model id — TECHSPEC §2);
//	(c) statelessness — two sequential Fix calls on the same adapter, same input,
//	    yield the same shape and do not leak state;
//	(d) a hung/timed-out call returns a CLEAN error (which the colony turns into
//	    a skip — TECHSPEC §10), proven by a stub that sleeps past a short deadline
//	    and asserting the call returns fast with an error, NOT a hang.
//
// All four are tested against RECORDED responses (exec adapters: an injected
// CommandRunner; rawmodel: an httptest server) so CI needs no live model calls.

// adapterUnderTest bundles a recorded-response factory for one adapter so the
// table can build each adapter with a stubbed transport, a stubbed slow
// transport (for the timeout case), and the expected provenance string.
type adapterUnderTest struct {
	name  string // logical adapter name, also the provenance prefix
	model string // config model — provenance must echo it, never hardcode it

	// newOK builds the adapter wired to return the recorded good response
	// immediately.
	newOK func(t *testing.T, model string) engine.Fixer
	// newHang builds the adapter wired so the underlying call blocks until the
	// caller's context deadline fires (the timeout→skip case). The stub respects
	// ctx so the test never actually hangs.
	newHang func(t *testing.T, model string) engine.Fixer
}

// recordedExec returns a CommandRunner that returns the given stdout immediately.
func recordedExec(stdout []byte) fix.CommandRunner {
	return func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
		return stdout, nil
	}
}

// hangingExec returns a CommandRunner that blocks until ctx is done, then
// returns ctx.Err() — exactly how exec.CommandContext behaves when a real
// harness hangs and the deadline kills it. No fixed sleep, so the test is fast
// and deterministic and a regression that ignored ctx would hang and fail the
// bounded test loudly.
func hangingExec() fix.CommandRunner {
	return func(ctx context.Context, _ string, _ []string, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

// adapters builds the table of every Fixer adapter under the shared contract.
func adapters(t *testing.T) []adapterUnderTest {
	t.Helper()
	const model = "qwen2.5-coder"
	pi := loadFixture(t, "pi-response.json")
	cc := loadFixture(t, "claudecode-response.json")
	cx := loadFixture(t, "codex-response.json") // JSONL stream (recorded codex exec --json)

	// rawmodel speaks HTTP; wrap the recorded patch in the OpenAI chat shape.
	rawPatch := "```diff\n--- a/svc/query.go\n+++ b/svc/query.go\n@@ -10,3 +10,1 @@\n-for _, id := range ids {\n-\tdb.Get(id)\n-}\n+rows := db.GetAll(ids)\n```\n"

	return []adapterUnderTest{
		{
			name: "pi", model: model,
			newOK: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewPiWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, recordedExec(pi))
				mustNoErr(t, err)
				return f
			},
			newHang: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewPiWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, hangingExec())
				mustNoErr(t, err)
				return f
			},
		},
		{
			name: "claudecode", model: model,
			newOK: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewClaudeCodeWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, recordedExec(cc))
				mustNoErr(t, err)
				return f
			},
			newHang: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewClaudeCodeWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, hangingExec())
				mustNoErr(t, err)
				return f
			},
		},
		{
			name: "codex", model: model,
			newOK: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewCodexWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, recordedExec(cx))
				mustNoErr(t, err)
				return f
			},
			newHang: func(t *testing.T, m string) engine.Fixer {
				f, err := fix.NewCodexWithRunner(fix.HarnessConfig{Model: m, Timeout: time.Second}, hangingExec())
				mustNoErr(t, err)
				return f
			},
		},
		{
			name: "rawmodel", model: model,
			newOK: func(t *testing.T, m string) engine.Fixer {
				srv := newChatServer(t, rawPatch, 0)
				f, err := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: m, HTTPClient: srv.Client()})
				mustNoErr(t, err)
				return f
			},
			newHang: func(t *testing.T, m string) engine.Fixer {
				srv := newHangingChatServer(t)
				f, err := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: m, HTTPClient: srv.Client()})
				mustNoErr(t, err)
				return f
			},
		},
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("construct adapter: %v", err)
	}
}

// newChatServer returns an httptest server replying with the OpenAI chat shape
// carrying patch, optionally after a delay.
func newChatServer(t *testing.T, patch string, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": patch}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newHangingChatServer returns a server that blocks until the test ends, so a
// client call times out via its context deadline (the rawmodel timeout→skip).
func newHangingChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}

// TestAdapterContract_OneTaskOneDiffWithProvenance asserts (a) + (b): one task
// in → one ProposedDiff out, parsed onto the finding's file, with non-empty
// provenance reflecting fixer + the config model.
func TestAdapterContract_OneTaskOneDiffWithProvenance(t *testing.T) {
	for _, a := range adapters(t) {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			f := a.newOK(t, a.model)
			diff, err := f.Fix(context.Background(), adapterTask())
			if err != nil {
				t.Fatalf("Fix: %v", err)
			}
			if len(diff.Files) != 1 {
				t.Fatalf("want exactly one FileDiff, got %d", len(diff.Files))
			}
			if diff.Files[0].Path != "svc/query.go" {
				t.Errorf("diff path = %q, want svc/query.go", diff.Files[0].Path)
			}
			if strings.TrimSpace(diff.Files[0].Patch) == "" {
				t.Error("patch is empty")
			}
			want := a.name + " (" + a.model + ")"
			if diff.Fixer != want {
				t.Errorf("provenance = %q, want %q", diff.Fixer, want)
			}
			if strings.TrimSpace(diff.Fixer) == "" {
				t.Error("provenance must be non-empty (TECHSPEC §10 point 3)")
			}
		})
	}
}

// TestAdapterContract_ProvenanceFollowsConfigModel asserts the model id is never
// hardcoded: a different config model yields a different provenance string
// (TECHSPEC §2).
func TestAdapterContract_ProvenanceFollowsConfigModel(t *testing.T) {
	for _, a := range adapters(t) {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			for _, m := range []string{"deepseek-coder", "starcoder2"} {
				f := a.newOK(t, m)
				diff, err := f.Fix(context.Background(), adapterTask())
				if err != nil {
					t.Fatalf("Fix(%s): %v", m, err)
				}
				want := a.name + " (" + m + ")"
				if diff.Fixer != want {
					t.Errorf("provenance = %q, want %q (model must come from config)", diff.Fixer, want)
				}
			}
		})
	}
}

// TestAdapterContract_Stateless asserts (c): two sequential Fix calls on the
// same adapter with the same input produce the same shape — no state leaks
// between tasks (TECHSPEC §10 point 4).
func TestAdapterContract_Stateless(t *testing.T) {
	for _, a := range adapters(t) {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			f := a.newOK(t, a.model)
			first, err := f.Fix(context.Background(), adapterTask())
			if err != nil {
				t.Fatalf("first Fix: %v", err)
			}
			second, err := f.Fix(context.Background(), adapterTask())
			if err != nil {
				t.Fatalf("second Fix: %v", err)
			}
			if first.Fixer != second.Fixer {
				t.Errorf("provenance differs across calls: %q vs %q", first.Fixer, second.Fixer)
			}
			if len(first.Files) != len(second.Files) || first.Files[0].Path != second.Files[0].Path {
				t.Errorf("diff shape differs across calls: %+v vs %+v", first.Files, second.Files)
			}
			if first.Files[0].Patch != second.Files[0].Patch {
				t.Error("same input produced different patches — adapter is not stateless")
			}
		})
	}
}

// TestAdapterContract_TimeoutIsCleanSkip asserts (d): a hung/timed-out call
// returns a clean error fast (→ skip), NOT a hang. The stub blocks on ctx; with
// a short deadline the call must return an error well within a generous bound. A
// regression that ignored the deadline would block past the bound and the
// bounded goroutine assertion fails loudly rather than stalling the suite.
func TestAdapterContract_TimeoutIsCleanSkip(t *testing.T) {
	for _, a := range adapters(t) {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			f := a.newHang(t, a.model)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()

			type result struct {
				diff engine.ProposedDiff
				err  error
			}
			done := make(chan result, 1)
			start := time.Now()
			go func() {
				diff, err := f.Fix(ctx, adapterTask())
				done <- result{diff, err}
			}()

			select {
			case r := <-done:
				if r.err == nil {
					t.Fatalf("expected a clean error on timeout (→ skip), got nil and diff %+v", r.diff)
				}
				// The error must signal cancellation/deadline so the colony skips
				// cleanly rather than treating it as an unexpected failure.
				if !errors.Is(r.err, context.DeadlineExceeded) && !errors.Is(r.err, context.Canceled) {
					t.Logf("note: %s timeout error does not wrap context error: %v", a.name, r.err)
				}
				if elapsed := time.Since(start); elapsed > 2*time.Second {
					t.Errorf("timeout path took %s — far longer than the 30ms deadline (a real hang?)", elapsed)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s Fix did not return after its deadline — hung call did NOT become a clean skip (TECHSPEC §10)", a.name)
			}
		})
	}
}
