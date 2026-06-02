# livewire-public-untyped-prop fix prompt (LLM-assisted)

The detected property is a PUBLIC, UNTYPED property on a Livewire component. Every
public property on a Livewire component is network-exposed state: it is serialized
to the browser and can be mutated by a crafted request. Without a type, the
property accepts any value the client sends, so a type-juggle or unexpected shape
can corrupt state or bypass a check.

Fix it by typing the property and locking it where mutation-sensitive:

1. Add the narrowest correct scalar/class type for the property based on its
   default value and usage (e.g. `public $count = 0;` → `public int $count = 0;`).
2. If the property is mutation-sensitive — an identifier, a total, an
   authorization-relevant flag that the client should NOT be able to change — add
   the `#[Locked]` attribute above it (and `use Livewire\Attributes\Locked;` if
   not already imported).
3. Do not change the property's default value or its name.

Constraints:
- Change only the property declaration (and the Locked import if you add it); do
  not touch unrelated code.
- The post-fix behavior must be identical for valid input and reject/coerce
  invalid client input via the new type.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the right type and
whether to lock are judgement calls. The verifier gate (detector-clears + a
`php -l` parse check) must pass: no untyped public property may remain, and the
file must still parse.
