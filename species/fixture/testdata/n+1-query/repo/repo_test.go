package nplusone

import (
	"reflect"
	"testing"
)

// TestNames pins Names' behavior across the fix: the N+1 loop and the batched
// rewrite must yield the same ordered result. tests:affected (TECHSPEC §5.3.1)
// selects this package and runs THIS test against the patched scratch tree, so
// the recorded batching fix is accepted only if it compiles and preserves
// behavior — the LLM species' propose-only fix proven safe without a live model.
func TestNames(t *testing.T) {
	got := Names([]int{1, 2, 3})
	want := []string{"ada", "linus", "grace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}
