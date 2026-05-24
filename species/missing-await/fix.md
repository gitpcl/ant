<!-- LLM fix prompt for the missing-await species (placeholder).
     The real prompt is authored in the M3 species-builtin sprint. -->
Add the missing await (or explicit promise handling) to the detected async call
so the result is not dropped. Preserve surrounding control flow and error
handling. Change only the localized span.
