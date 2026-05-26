// handle has a marked empty if-body — likely a forgotten body, so the species
// proposes removing the empty statement but stays PROPOSE-ONLY (never auto-
// applied; a human reviews whether the body was meant to be filled in). The
// if-statement with a real body below has no marker and is untouched.
export function handle(x: number): number {
  // ant:empty-block forgotten body?
  if (x > 0) {}
  if (x < 0) {
    return -1;
  }
  return 0;
}
