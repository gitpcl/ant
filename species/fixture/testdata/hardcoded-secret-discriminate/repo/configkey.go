// This file is a NON-MATCH: the literal assigned to the `Key...`-named const is
// a lowercase dot/underscore-segmented CONFIG-KEY PATH (a viper key), not a
// secret value. It clears the length/entropy gate (entropy ~4.0 bits/char), so
// it reaches the exclusion; the `^[a-z][a-z0-9_]*([._][a-z0-9_]+)+$`
// config-key-path shape excludes it. It must NOT be flagged.
package discriminate

// KeyVerifyMaxChangedLines is the viper key for the diff-bounded line cap.
const KeyVerifyMaxChangedLines = "verify.max_changed_lines"
