#!/bin/sh
# unused-dependency verifier (Sprint 020 command: escape hatch).
#
# Runs in the SCRATCH COPY of the tree (verify/command.go applies the proposed
# diff to a throwaway copy first), so this proves the project still builds and
# vets AFTER the dependency was removed from go.mod — the "install + compile/
# tests" gate for Go. A non-zero exit fails the gate, so a removal that breaks the
# build (the dependency was actually used) is skipped and never staged.
#
# Hermetic & offline: the fixture module has NO external dependencies once the
# unused require is removed, so `go build`/`go vet` resolve entirely from the
# local module + the standard library — no network, no module download. We force
# offline mode so a stray attempted download fails loudly rather than hanging CI.
set -eu

export GOFLAGS=-mod=mod
export GOPROXY=off
export GO111MODULE=on

# Compile + vet the whole module. Either failing is a failed gate (the removed
# dependency was needed → the fix is unsafe → skip).
go build ./...
go vet ./...
