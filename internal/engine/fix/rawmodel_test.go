package fix_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

func rawModelTask() engine.FixTask {
	return engine.FixTask{
		Finding: engine.Finding{
			Species:  "n+1-query",
			File:     "svc/query.go",
			Span:     engine.Span{StartLine: 10, EndLine: 14},
			Severity: engine.SeverityHigh,
			Message:  "1+N query in loop",
			Snippet:  "for _, id := range ids { db.Get(id) }",
		},
		Context: engine.CodeContext{
			File:     "svc/query.go",
			Language: "go",
			Span:     engine.Span{StartLine: 10, EndLine: 14},
			Snippet:  "for _, id := range ids { db.Get(id) }",
		},
		Prompt: "Batch the queries.",
	}
}

// TestRawModelPostsParsesAndSetsProvenance is the core acceptance test: against
// an httptest server (no live model), the adapter posts to the CONFIGURED
// endpoint, sends the model from config, parses the returned diff, and sets
// provenance from the config model — never a hardcoded model id.
func TestRawModelPostsParsesAndSetsProvenance(t *testing.T) {
	const (
		wantModel = "qwen2.5-coder" // comes from config, asserted in the request
		wantPath  = "/v1/chat/completions"
	)
	returnedPatch := "--- a/svc/query.go\n+++ b/svc/query.go\n@@ -10,5 +10,3 @@\n-for _, id := range ids { db.Get(id) }\n+rows := db.GetAll(ids)\n"

	var (
		gotPath   string
		gotModel  string
		gotMethod string
		gotAuth   string
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		gotModel = req.Model

		// Reply in the OpenAI chat-completions shape, wrapping the diff in a fence
		// (a common model behavior the adapter must unwrap).
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "```diff\n" + returnedPatch + "```"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	fixer, err := fix.NewRawModel(fix.RawModelConfig{
		Endpoint:   srv.URL + wantPath,
		Model:      wantModel,
		APIKey:     "test-key",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewRawModel: %v", err)
	}

	diff, err := fixer.Fix(context.Background(), rawModelTask())
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	// Posted to the configured endpoint path, as a POST, with the config model.
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != wantPath {
		t.Errorf("posted to %q, want %q", gotPath, wantPath)
	}
	if gotModel != wantModel {
		t.Errorf("request model = %q, want %q (model must come from config)", gotModel, wantModel)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	// The one-task prompt carried the finding + instructions (TECHSPEC §10).
	if !strings.Contains(gotBody, "1+N query in loop") || !strings.Contains(gotBody, "Batch the queries.") {
		t.Errorf("request body missing localized task content:\n%s", gotBody)
	}

	// Parsed the diff (fence unwrapped) onto the finding's file.
	if len(diff.Files) != 1 || diff.Files[0].Path != "svc/query.go" {
		t.Fatalf("file diff = %+v, want one diff for svc/query.go", diff.Files)
	}
	if diff.Files[0].Patch != returnedPatch {
		t.Errorf("parsed patch mismatch:\n got:\n%q\nwant:\n%q", diff.Files[0].Patch, returnedPatch)
	}

	// Provenance reflects the CONFIG model, not a literal.
	if diff.Fixer != "rawmodel (qwen2.5-coder)" {
		t.Errorf("provenance = %q, want %q", diff.Fixer, "rawmodel (qwen2.5-coder)")
	}
}

// TestRawModelProvenanceFollowsConfigModel proves provenance tracks the
// configured model rather than any fixed string: a different config model yields
// a different provenance, demonstrating the model is never hardcoded.
func TestRawModelProvenanceFollowsConfigModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]string{
			"content": "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n",
		}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	for _, model := range []string{"deepseek-coder", "starcoder2", "phi-3"} {
		fixer, err := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: model, HTTPClient: srv.Client()})
		if err != nil {
			t.Fatalf("NewRawModel(%s): %v", model, err)
		}
		diff, err := fixer.Fix(context.Background(), rawModelTask())
		if err != nil {
			t.Fatalf("Fix(%s): %v", model, err)
		}
		want := "rawmodel (" + model + ")"
		if diff.Fixer != want {
			t.Errorf("provenance = %q, want %q", diff.Fixer, want)
		}
	}
}

func TestRawModelRejectsMissingConfig(t *testing.T) {
	t.Run("no endpoint", func(t *testing.T) {
		if _, err := fix.NewRawModel(fix.RawModelConfig{Model: "m"}); err == nil {
			t.Error("expected an error when endpoint is empty")
		}
	})
	t.Run("no model", func(t *testing.T) {
		// A missing model must error, NOT silently default to a hardcoded id
		// (TECHSPEC §2).
		if _, err := fix.NewRawModel(fix.RawModelConfig{Endpoint: "http://x"}); err == nil {
			t.Error("expected an error when model is empty (model must never be hardcoded)")
		}
	})
}

// TestRawModelErrorPathsAreCleanSkips proves transport/HTTP/parse failures
// return an error (which the colony turns into a skip — TECHSPEC §10), never a
// panic.
func TestRawModelErrorPathsAreCleanSkips(t *testing.T) {
	t.Run("non-2xx status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "model overloaded", http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		fixer, _ := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: "m", HTTPClient: srv.Client()})
		if _, err := fixer.Fix(context.Background(), rawModelTask()); err == nil {
			t.Error("expected an error on a non-2xx response")
		}
	})

	t.Run("no choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"choices":[]}`)
		}))
		defer srv.Close()
		fixer, _ := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: "m", HTTPClient: srv.Client()})
		if _, err := fixer.Fix(context.Background(), rawModelTask()); err == nil {
			t.Error("expected an error when the response has no choices")
		}
	})

	t.Run("context timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release // hang until the client gives up
		}))
		defer srv.Close()
		defer close(release)

		fixer, _ := fix.NewRawModel(fix.RawModelConfig{Endpoint: srv.URL, Model: "m", HTTPClient: srv.Client()})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := fixer.Fix(ctx, rawModelTask()); err == nil {
			t.Error("expected an error when the call times out (hung endpoint → clean skip)")
		}
	})
}
