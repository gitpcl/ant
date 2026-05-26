// guard returns early; the marked statement after the return can never execute,
// so the species deletes it. The indented delete-match removes the verbatim line
// (leading whitespace intact) and detector-clears confirms it is gone. The
// reachable `return 0;` below has no marker and is left alone.
export function guard(x: number): number {
  if (x > 0) {
    return x;
    // ant:unreachable-code dead statement after return
    console.log("never runs");
  }
  return 0;
}
