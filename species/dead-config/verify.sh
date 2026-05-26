#!/bin/sh
# dead-config verifier (Sprint 020 command: escape hatch).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves the config file is STILL VALID JSON after the dead key was removed —
# the config-parse gate the contract requires. A removal that left trailing-comma
# or brace damage produces invalid JSON, the parse exits non-zero, and the fix is
# skipped (never staged).
#
# Hermetic & offline: pure stdlib JSON parse via python3 (no network, no
# packages). The fixture harness skips this species' case when python3 is absent
# (RequiredTools), exactly as ast-grep species skip without the matcher — so CI
# without python3 stays green while the gate runs for real where present.
set -eu

python3 -c 'import json,sys; json.load(open("config.json"))'
