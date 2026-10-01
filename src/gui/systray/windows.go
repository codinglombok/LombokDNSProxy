// Package systray — Windows DNS configuration
// Uses PowerShell Set-DnsClientServerAddress to configure DNS per adapter.
// Requires administrator privileges (prompted via UAC if needed).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
//go:build windows

package systray

import (
	"fmt"
	"os/exec"
	"strings"
)

type windowsConfigurator struct {
	original map[string][]string // adapter → original DNS servers
}

// Set configures Windows DNS using PowerShell.
func (c *windowsConfigurator) Set(cfg DNSConfig) error {
	adapters, err := activeAdapters()
	if err != nil {
		return fmt.Errorf("systray/windows: list adapters: %w", err)
	}

	c.original = make(map[string][]string)
	for _, adapter := range adapters {
		current, _ := dnsForAdapter(adapter)
		c.original[adapter] = current

		ps := fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses ('%s')",
			adapter, cfg.ListenAddr)
		if err := runPS(ps); err != nil {
			continue // non-fatal for individual adapters
		}
	}

	// Flush Windows DNS cache
	exec.Command("ipconfig", "/flushdns").Run() //nolint:errcheck
	return nil
}

// Restore returns each adapter to its original DNS configuration.
func (c *windowsConfigurator) Restore() error {
	for adapter, servers := range c.original {
		var ps string
		if len(servers) == 0 {
			ps = fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses", adapter)
		} else {
			joined := "'" + strings.Join(servers, "','") + "'"
			ps = fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses (%s)", adapter, joined)
		}
		runPS(ps) //nolint:errcheck
	}
	exec.Command("ipconfig", "/flushdns").Run() //nolint:errcheck
	return nil
}

// Current returns the DNS servers for the primary active adapter.
func (c *windowsConfigurator) Current() ([]string, error) {
	adapters, err := activeAdapters()
	if err != nil || len(adapters) == 0 {
		return nil, err
	}
	return dnsForAdapter(adapters[0])
}

// activeAdapters returns the names of active (connected) network adapters.
func activeAdapters() ([]string, error) {
	ps := "Get-NetAdapter | Where-Object {$_.Status -eq 'Up'} | Select-Object -ExpandProperty Name"
	out, err := runPSOutput(ps)
	if err != nil {
		return nil, err
	}
	var adapters []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			adapters = append(adapters, line)
		}
	}
	return adapters, nil
}

// dnsForAdapter returns the current DNS servers for a given adapter.
func dnsForAdapter(adapter string) ([]string, error) {
	ps := fmt.Sprintf(
		"(Get-DnsClientServerAddress -InterfaceAlias '%s' -AddressFamily IPv4).ServerAddresses",
		adapter,
	)
	out, err := runPSOutput(ps)
	if err != nil {
		return nil, err
	}
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			servers = append(servers, line)
		}
	}
	return servers, nil
}

// runPS runs a PowerShell command without capturing output.
func runPS(script string) error {
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

// runPSOutput runs a PowerShell command and returns its stdout.
func runPSOutput(script string) (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
