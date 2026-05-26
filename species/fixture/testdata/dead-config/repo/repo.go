// Package deadconfig is a hermetic fixture for the dead-config species. Its
// config.json declares three keys; this source references the two LIVE keys by
// name (so the detector's cross-file usage check finds them) but never the orphan
// key, which was left behind after its consuming code was deleted. The detector
// flags the orphan key line; the deterministic delete-match fix removes it (its
// trailing comma goes with the line, so the JSON stays valid); the command:
// verifier parses config.json to prove it is still valid JSON after removal.
package deadconfig

// configKeys are the config.json keys this code actually reads. The string
// literals make those keys "referenced" for the detector's usage check, so only
// the genuinely-orphan key (named only in config.json) is flagged.
var configKeys = []string{"activeTimeout", "serviceName"}

// Keys returns the live config keys.
func Keys() []string { return configKeys }
