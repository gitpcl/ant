#!/bin/sh
# stale-dependency-pin verifier (Sprint 020 command: escape hatch).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves the module still builds and vets AFTER the redundant pin was removed
# — the "install + tests:affected" gate for Go. A non-zero exit fails the gate, so
# a removal that breaks the build (the kept pin was insufficient) is skipped.
#
# Hermetic & offline: the fixture module has NO external dependencies, so
# `go build`/`go vet` resolve from the local module + stdlib alone. GOPROXY=off
# makes any stray download attempt fail loudly rather than hang CI.
set -eu

export GOFLAGS=-mod=mod
export GOPROXY=off
export GO111MODULE=on

go build ./...
go vet ./...
