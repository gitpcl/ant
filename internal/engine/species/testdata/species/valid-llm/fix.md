Rewrite the N+1 query so the data is fetched in a single batched query before
the loop, preserving the original ordering and error handling. Change only the
localized span; do not touch unrelated code.
