// read dereferences `o` on the first line (o!.v), so by the time the marked
// `if (o === null)` check runs, o cannot be null — the check is impossible. The
// species proposes deleting it but stays PROPOSE-ONLY (removing a guard is a
// human decision). The genuine bounds check below has no marker and is untouched.
export function read(o: { v: number } | null, limit: number): number {
  const v = o!.v;
  // ant:redundant-nil-check o was already dereferenced above; cannot be null here
  if (o === null) return 0;
  if (v > limit) return limit;
  return v;
}
