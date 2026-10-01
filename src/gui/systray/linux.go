// Package systray — Linux DNS configuration
// Uses systemd-resolved stub (preferred) with /etc/resolv.conf fallback.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
//go:build linux

package systray

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const resolvConf = "/etc/resolv.conf"
const resolvBackup = "/etc/resolv.conf.lombok-backup"

type linuxConfigurator struct {
	usedSystemd bool
}

// Set configures Linux DNS via systemd-resolved or /etc/resolv.conf.
func (c *linuxConfigurator) Set(cfg DNSConfig) error {
	// Try systemd-resolved first (modern Linux)
	if isSystemdResolvedActive() {
		return c.setSystemd(cfg)
	}
	return c.setResolvConf(cfg)
}

// Restore returns DNS to its original configuration.
func (c *linuxConfigurator) Restore() error {
	if c.usedSystemd {
		return c.restoreSystemd()
	}
	return c.restoreResolvConf()
}

// Current returns the current system DNS nameservers.
func (c *linuxConfigurator) Current() ([]string, error) {
	data, err := os.ReadFile(resolvConf)
	if err != nil {
		return nil, err
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver ") {
			servers = append(servers, strings.TrimPrefix(line, "nameserver "))
		}
	}
	return servers, nil
}

// setSystemd uses systemd-resolved's resolvectl to set the DNS server.
func (c *linuxConfigurator) setSystemd(cfg DNSConfig) error {
	// Set DNS for all network interfaces via resolvectl
	ifaces, err := activeInterfaces()
	if err != nil || len(ifaces) == 0 {
		ifaces = []string{"eth0", "wlan0"} // best-guess fallback
	}

	for _, iface := range ifaces {
		addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
		if err := exec.Command("resolvectl", "dns", iface, addr).Run(); err != nil {
			// Non-fatal — some interfaces may not exist
			continue
		}
	}

	// Also set stub resolver to point to us
	stubPath := "/etc/systemd/resolved.conf.d/lombok-dns.conf"
	os.MkdirAll("/etc/systemd/resolved.conf.d/", 0o755) //nolint:errcheck
	content := fmt.Sprintf("[Resolve]\nDNS=%s:%d\nDNSStubListener=yes\n",
		cfg.ListenAddr, cfg.Port)
	if err := os.WriteFile(stubPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("systray/linux: write resolved conf: %w", err)
	}

	exec.Command("systemctl", "restart", "systemd-resolved").Run() //nolint:errcheck
	c.usedSystemd = true
	return nil
}

// restoreSystemd removes the LombokDNSProxy override and restores defaults.
func (c *linuxConfigurator) restoreSystemd() error {
	os.Remove("/etc/systemd/resolved.conf.d/lombok-dns.conf") //nolint:errcheck
	exec.Command("systemctl", "restart", "systemd-resolved").Run() //nolint:errcheck
	return nil
}

// setResolvConf directly replaces /etc/resolv.conf.
func (c *linuxConfigurator) setResolvConf(cfg DNSConfig) error {
	// Back up original
	original, err := os.ReadFile(resolvConf)
	if err != nil {
		return err
	}
	if err := os.WriteFile(resolvBackup, original, 0o644); err != nil {
		return fmt.Errorf("systray/linux: backup resolv.conf: %w", err)
	}

	content := fmt.Sprintf("# LombokDNSProxy — auto-generated\nnameserver %s\n", cfg.ListenAddr)
	if err := os.WriteFile(resolvConf, []byte(content), 0o644); err != nil {
		return fmt.Errorf("systray/linux: write resolv.conf: %w", err)
	}
	c.usedSystemd = false
	return nil
}

// restoreResolvConf restores the backed-up /etc/resolv.conf.
func (c *linuxConfigurator) restoreResolvConf() error {
	data, err := os.ReadFile(resolvBackup)
	if err != nil {
		return fmt.Errorf("systray/linux: no backup found: %w", err)
	}
	if err := os.WriteFile(resolvConf, data, 0o644); err != nil {
		return err
	}
	return os.Remove(resolvBackup)
}

func isSystemdResolvedActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run() == nil
}

func activeInterfaces() ([]string, error) {
	out, err := exec.Command("ip", "-o", "link", "show", "up").Output()
	if err != nil {
		return nil, err
	}
	var ifaces []string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSuffix(parts[1], ":")
		if name != "lo" {
			ifaces = append(ifaces, name)
		}
	}
	return ifaces, nil
}
