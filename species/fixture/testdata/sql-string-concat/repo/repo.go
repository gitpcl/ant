package sqlstringconcat

import "strconv"

// querier models the database/sql QueryRow contract: a static query string plus
// bound parameter values. A real *sql.DB satisfies this shape. The fake in
// repo_test.go records the query text and the bound args so the test can prove
// the post-fix code binds the value as data instead of splicing it into SQL.
type querier interface {
	QueryRow(query string, args ...any) row
}

// row models the *sql.Row Scan contract.
type row interface {
	Scan(dest ...any) error
}

// Store is a thin data-access layer over a querier (a *sql.DB in production).
type Store struct {
	db querier
}

// NewStore wires a Store to its querier.
func NewStore(db querier) *Store {
	return &Store{db: db}
}

// UserName is the sql-string-concat smell: it builds the query by CONCATENATING
// the caller-supplied id straight into the SQL text (`"... WHERE id = " +
// strconv.Itoa(id)`). The value is interpreted as SQL, not as data, so a crafted
// id can rewrite the statement — the canonical SQL-injection vector. The
// sql-string-concat species nominates the concatenated query string, the recorded
// fix moves the id into a bound `?` parameter, and the verifier gate (compile +
// tests:affected + detector-clears) confirms it.
func (s *Store) UserName(id int) (string, error) {
	var name string
	r := s.db.QueryRow("SELECT name FROM users WHERE id = " + strconv.Itoa(id))
	if err := r.Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}
