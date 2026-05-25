package nilderef

import "errors"

// Account is the value loadAccount yields alongside an error.
type Account struct{ Balance int }

// loadAccount models a fallible lookup: it returns a (*Account, error) pair, so
// a caller that ignores the error can be left holding a nil *Account.
func loadAccount(id int) (*Account, error) {
	if id <= 0 {
		return nil, errors.New("no such account")
	}
	return &Account{Balance: id * 10}, nil
}

// Balance is the nil-deref smell: it discards loadAccount's error with `_` and
// then dereferences acct.Balance. On the error path acct is nil, so the
// dereference panics. The nil-deref species nominates this (a pointer from a
// call whose error was dropped), the recorded fix binds and checks err, and the
// verifier gate (compile + tests:affected + detector-clears) confirms it.
func Balance(id int) int {
	acct, _ := loadAccount(id)
	return acct.Balance
}
