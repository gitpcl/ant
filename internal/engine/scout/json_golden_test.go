package scout

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/events"
)

// goldenPath is the committed --json event-stream contract. Front doors
// (Sprint 013) parse exactly this shape, so a change to it must break this test
// (TECHSPEC §12). Regenerate intentionally with UPDATE_GOLDEN=1.
const goldenPath = "testdata/scout-json.golden"

// fixedClock returns a deterministic clock so the golden stream is byte-stable
// (timestamps would otherwise vary every run).
func fixedClock() func() time.Time {
	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return ts }
}

// TestScoutJSONGolden runs a fixed scout fixture through the SAME event bus and
// --json renderer the CLI uses, then asserts the stream matches the committed
// golden. The detectors replay the recorded ast-grep payload through the real
// parse + mapping path, so the golden reflects the genuine Finding shape, not a
// hand-built one. Human and --json are the same run rendered two ways; this
// pins the machine-readable rendering.
func TestScoutJSONGolden(t *testing.T) {
	got := renderGoldenStream(t)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s (run with UPDATE_GOLDEN=1 to create): %v", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("--json event stream drifted from the golden contract.\n--- got ---\n%s\n--- want ---\n%s\nRegenerate intentionally with UPDATE_GOLDEN=1 if this change is deliberate.",
			got, want)
	}
}

// renderGoldenStream produces the deterministic --json stream for the fixture:
// fixed clock, fixed RunID, recorded detector. It mirrors scout.Drive but with
// the deterministic bus so it can be captured byte-for-byte.
func renderGoldenStream(t *testing.T) []byte {
	t.Helper()
	bus := events.NewBus(events.WithClock(fixedClock()))
	sub := bus.Subscribe()

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- events.RenderJSON(&buf, sub) }()

	opts := Options{
		Scope:     engine.Scope{Root: "testdata/has-findings"},
		Detectors: []engine.NamedDetector{{Species: "unused-import", Detector: detect.NewRecorded("unused-import", recordedFixture(t))}},
		RunID:     "golden-run",
		Now:       fixedClock(),
	}
	if _, err := Run(context.Background(), bus, opts); err != nil {
		t.Fatalf("scout Run: %v", err)
	}
	bus.Close()
	if err := <-done; err != nil {
		t.Fatalf("render json: %v", err)
	}
	return buf.Bytes()
}
