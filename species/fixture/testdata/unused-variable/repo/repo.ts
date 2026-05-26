// sum returns a + b. The marked local `scratch` is computed and then never read.
// TypeScript does not treat an unused local as a compile error, so this is the
// meaningful in-function target for the indented delete-match path: the fix
// removes the indented `const` declaration with its leading whitespace intact,
// and detector-clears proves the declaration is gone (compile is a vacuous pass
// on this Go-module-only fixture, which has no TS toolchain wired).
export function sum(a: number, b: number): number {
  // ant:unused-variable scratch is computed but never read
  const scratch = a * b;
  return a + b;
}
