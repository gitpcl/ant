<!-- LLM fix prompt for the n+1-query species (placeholder).
     The real prompt is authored in the M3 species-builtin sprint. -->
Rewrite the detected N+1 query so the data is fetched in a single batched query
before the loop. Preserve ordering and error handling. Change only the localized
span; do not touch unrelated code.
