// label wraps a value that is ALREADY a string in String(), a redundant
// conversion the author marked for removal. The deterministic rewrite replaces
// `String(n)` with `n` (the ast-grep fix: output), preserving the line's
// indentation, and detector-clears confirms the String(...) shape is gone.
export function label(n: string): string {
  // ant:redundant-conversion n is already a string
  return String(n);
}

// describe converts a genuine number to a string — NOT redundant and NOT marked,
// so the species leaves it untouched (proving the marker scopes the rewrite to
// author-nominated conversions only).
export function describe(count: number): string {
  return String(count);
}
