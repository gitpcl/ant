package engine

// Version is the engine library version. The CLI renders it; it is owned here
// (not in cmd/ant) so the version is a property of the engine the enterprise
// layer also imports, not of the thin CLI shell (TECHSPEC §3).
const Version = "0.1.0-dev"
