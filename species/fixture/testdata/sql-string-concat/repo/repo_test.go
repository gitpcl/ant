package sqlstringconcat

import (
	"errors"
	"strings"
	"testing"
)

// fakeRow returns a fixed name (or an error) from Scan, modelling *sql.Row.
type fakeRow struct {
	name string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if p, ok := dest[0].(*string); ok {
			*p = r.name
		}
	}
	return nil
}

// fakeDB records the query text and the bound args passed to QueryRow, so the
// test can prove the post-fix code sends a STATIC query plus a BOUND parameter
// (the parameterized shape) rather than a query string with the id concatenated
// in. It returns a preset row.
type fakeDB struct {
	gotQuery string
	gotArgs  []any
	result   fakeRow
}

func (f *fakeDB) QueryRow(query string, args ...any) row {
	f.gotQuery = query
	f.gotArgs = args
	return f.result
}

// TestUserNameUsesBoundParameter exercises the POST-FIX UserName. tests:affected
// (TECHSPEC §5.3.1) selects this package and runs THIS test against the patched
// scratch tree, so the recorded parameterized fix is accepted only if it both
// compiles and keeps this behavior green. The assertions pass ONLY for the
// parameterized shape: the query must be a static literal carrying a placeholder
// (no id value spliced into the SQL text), and the id must arrive as a bound
// argument — proving the SQL-injection vector is closed.
func TestUserNameUsesBoundParameter(t *testing.T) {
	db := &fakeDB{result: fakeRow{name: "ada"}}
	s := NewStore(db)

	got, err := s.UserName(42)
	if err != nil {
		t.Fatalf("UserName(42) returned unexpected error: %v", err)
	}
	if want := "ada"; got != want {
		t.Fatalf("UserName(42) = %q, want %q", got, want)
	}

	// The query text must NOT contain the concrete id — the value must be bound,
	// not concatenated into the SQL. A placeholder ("?" or "$1") must be present.
	if strings.Contains(db.gotQuery, "42") {
		t.Fatalf("query text spliced the id in (SQL-injection vector): %q", db.gotQuery)
	}
	if !strings.Contains(db.gotQuery, "?") && !strings.Contains(db.gotQuery, "$1") {
		t.Fatalf("query text has no bound-parameter placeholder: %q", db.gotQuery)
	}

	// The id must be passed as a bound argument (data), not interpolated.
	if len(db.gotArgs) != 1 {
		t.Fatalf("expected exactly one bound arg, got %d: %v", len(db.gotArgs), db.gotArgs)
	}
	if db.gotArgs[0] != 42 {
		t.Fatalf("bound arg = %v, want 42", db.gotArgs[0])
	}
}

// TestUserNameError confirms the error path is preserved by the fix: a Scan error
// is propagated, never swallowed.
func TestUserNameError(t *testing.T) {
	wantErr := errors.New("no rows")
	db := &fakeDB{result: fakeRow{err: wantErr}}
	s := NewStore(db)

	if _, err := s.UserName(7); !errors.Is(err, wantErr) {
		t.Fatalf("UserName(7) error = %v, want %v", err, wantErr)
	}
}
