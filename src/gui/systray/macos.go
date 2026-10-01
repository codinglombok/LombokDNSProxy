// Package systray — macOS DNS configuration
// Uses networksetup to configure DNS per network service.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
//go:build darwin

package systray

import (
	"fmt"
	"os/exec"
	"strings"
)

type macOSConfigurator struct {
	original map[string][]string // service → original DNS servers
}

// Set configures macOS DNS using networksetup.
func (c *macOSConfigurator) Set(cfg DNSConfig) error {
	services, err := networkServices()
	if err != nil {
		return fmt.Errorf("systray/macos: list services: %w", err)
	}

	c.original = make(map[string][]string)
	for _, svc := range services {
		// Save original DNS
		current, _ := dnsForService(svc)
		c.original[svc] = current

		// Set to LombokDNSProxy
		if err := exec.Command("networksetup", "-setdnsservers", svc, cfg.ListenAddr).Run(); err != nil {
			// Some services may be read-only; skip
			continue
		}
	}
	// Flush DNS cache
	exec.Command("dscacheutil", "-flushcache").Run()           //nolint:errcheck
	exec.Command("killall", "-HUP", "mDNSResponder").Run()     //nolint:errcheck
	return nil
}

// Restore returns each network service to its original DNS configuration.
func (c *macOSConfigurator) Restore() error {
	for svc, servers := range c.original {
		args := append([]string{"-setdnsservers", svc}, servers...)
		if len(servers) == 0 {
			args = []string{"-setdnsservers", svc, "empty"}
		}
		exec.Command("networksetup", args...).Run() //nolint:errcheck
	}
	exec.Command("dscacheutil", "-flushcache").Run()       //nolint:errcheck
	exec.Command("killall", "-HUP", "mDNSResponder").Run() //nolint:errcheck
	return nil
}

// Current returns the DNS servers for the primary network service.
func (c *macOSConfigurator) Current() ([]string, error) {
	services, err := networkServices()
	if err != nil || len(services) == 0 {
		return nil, err
	}
	return dnsForService(services[0])
}

// networkServices returns all active macOS network services.
func networkServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

// dnsForService returns the current DNS servers for a given network service.
func dnsForService(service string) ([]string, error) {
	out, err := exec.Command("networksetup", "-getdnsservers", service).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if strings.Contains(raw, "There aren't any DNS Servers") {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
