// compute demonstrates BOTH variants the sprint requires:
//
//   * `let x = 1` is a TRIVIAL ineffective assignment — its literal RHS is
//     overwritten by `x = 2` before any read. The RHS is side-effect-free, so the
//     species nominates it (and proposes deleting the declaration line). It still
//     stays PROPOSE-ONLY: auto_apply = false, so even under `--apply` it is staged
//     for review, never auto-landed.
//
//   * `let y = sideEffect()` is the SIDE-EFFECTING variant — overwritten by
//     `y = 3` before read, but its RHS calls sideEffect(), so removing the
//     declaration would drop the call. The detector's literal-RHS constraint does
//     NOT match it, so it is left entirely untouched (never even staged).
export function compute(): number {
  // ant:ineffective-assignment x literal assignment overwritten before read
  let x = 1;
  x = 2;
  // ant:ineffective-assignment y has a side-effecting RHS; must NOT be nominated
  let y = sideEffect();
  y = 3;
  return x + y;
}

function sideEffect(): number {
  return 9;
}
