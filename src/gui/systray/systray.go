// Package systray — LombokDNSProxy · OS DNS Configuration
// Auto-configures the operating system to use LombokDNSProxy as the
// system DNS resolver. Platform-specific implementations in build-tagged files.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package systray

import (
	"fmt"
	"runtime"
)

// DNSConfig holds the parameters for OS DNS configuration.
type DNSConfig struct {
	ListenAddr string   // e.g. "127.0.0.1"
	Port       int      // e.g. 53
	Servers    []string // DNS server addresses to set
}

// Configurator is the interface for platform-specific DNS configuration.
type Configurator interface {
	// Set configures the OS to use LombokDNSProxy as the DNS resolver.
	Set(cfg DNSConfig) error
	// Restore returns the OS to its original DNS configuration.
	Restore() error
	// Current returns the current system DNS servers.
	Current() ([]string, error)
}

// New returns the appropriate Configurator for the current OS.
func New() (Configurator, error) {
	switch runtime.GOOS {
	case "linux":
		return &linuxConfigurator{}, nil
	case "darwin":
		return &macOSConfigurator{}, nil
	case "windows":
		return &windowsConfigurator{}, nil
	default:
		return nil, fmt.Errorf("systray: unsupported OS: %s", runtime.GOOS)
	}
}

// DefaultDNSConfig returns a DNSConfig pointing to the local daemon.
func DefaultDNSConfig() DNSConfig {
	return DNSConfig{
		ListenAddr: "127.0.0.1",
		Port:       53,
		Servers:    []string{"127.0.0.1"},
	}
}
