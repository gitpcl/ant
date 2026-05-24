<!-- LLM fix prompt for the nil-deref species (placeholder).
     The real prompt is authored in the M3 species-builtin sprint. -->
Guard the detected dereference against a nil/null value with a minimal,
idiomatic check that preserves existing behavior on the non-nil path. Change
only the localized span.
