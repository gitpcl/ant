// classify has a second `if (x > 0)` that can never be reached — the first
// branch already returns for that condition. The species proposes deleting the
// marked duplicate branch but stays PROPOSE-ONLY (a human confirms which branch
// was intended). The distinct `if (x < 0)` has no marker and is untouched.
export function classify(x: number): number {
  if (x > 0) return 1;
  if (x < 0) return -1;
  // ant:duplicate-condition repeats the earlier `x > 0` branch
  if (x > 0) return 2;
  return 0;
}
