// Package build — LombokDNSProxy build-time metadata.
// Variables are injected at compile time via -ldflags.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package build

import "fmt"

// These are set by the linker: -ldflags "-X github.com/codinglombok/lombok-dns-proxy/internal/build.Version=v1.0.0"
var (
	Version   = "v0.0.0-dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// String returns a human-readable build string.
func String() string {
	return fmt.Sprintf("LombokDNSProxy %s (built %s, commit %s)", Version, BuildTime, GitCommit)
}
