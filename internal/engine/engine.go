package engine

// Version is the engine library version. The CLI renders it; it is owned here
// (not in cmd/ant) so the version is a property of the engine the enterprise
// layer also imports, not of the thin CLI shell (TECHSPEC §3).
//
// It is a var (not a const) so a release build can inject the real tag via
// linker flags: goreleaser sets `-X github.com/gitpcl/ant/internal/engine.Version=<tag>`
// (see .goreleaser.yaml). From source (`go run`/`go build`) it stays "dev".
var Version = "dev"
