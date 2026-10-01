// Package server — LombokDNSProxy · OS DNS Auto-Configuration
// Called by Server.Run() to optionally set the OS system DNS to point
// to the local LombokDNSProxy daemon, and restore it on shutdown.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package server

import (
	"context"

	"go.uber.org/zap"

	"github.com/codinglombok/lombok-dns-proxy/src/gui/systray"
)

// OSConfig controls whether and how the server auto-configures the OS DNS.
type OSConfig struct {
	SetSystemDNS   bool   // if true, configure OS DNS on start
	RestoreOnExit  bool   // if true, restore original DNS on shutdown
	ListenAddr     string // address daemon listens on
	Port           int    // DNS port
}

// osDNSManager wraps the systray.Configurator and manages lifecycle.
type osDNSManager struct {
	configurator systray.Configurator
	cfg          OSConfig
	log          *zap.Logger
}

// newOSDNSManager creates a manager for the current OS.
// Returns nil (not an error) if SetSystemDNS is false.
func newOSDNSManager(cfg OSConfig, log *zap.Logger) (*osDNSManager, error) {
	if !cfg.SetSystemDNS {
		return nil, nil
	}
	c, err := systray.New()
	if err != nil {
		// Non-fatal: log and continue without OS DNS config
		log.Warn("OS DNS configurator unavailable", zap.Error(err))
		return nil, nil
	}
	return &osDNSManager{configurator: c, cfg: cfg, log: log}, nil
}

// Set configures the OS DNS to point to the local daemon.
func (m *osDNSManager) Set() {
	if m == nil {
		return
	}
	dnsCfg := systray.DNSConfig{
		ListenAddr: m.cfg.ListenAddr,
		Port:       m.cfg.Port,
		Servers:    []string{m.cfg.ListenAddr},
	}
	if err := m.configurator.Set(dnsCfg); err != nil {
		m.log.Warn("failed to set system DNS", zap.Error(err))
		return
	}
	m.log.Info("system DNS configured",
		zap.String("addr", m.cfg.ListenAddr),
		zap.Int("port", m.cfg.Port),
	)
}

// Restore returns the OS to its original DNS configuration.
// Called automatically on graceful shutdown if RestoreOnExit is true.
func (m *osDNSManager) Restore() {
	if m == nil || !m.cfg.RestoreOnExit {
		return
	}
	if err := m.configurator.Restore(); err != nil {
		m.log.Warn("failed to restore system DNS", zap.Error(err))
		return
	}
	m.log.Info("system DNS restored to original")
}

// RunWithOSDNS wraps a server run function, setting/restoring OS DNS around it.
func RunWithOSDNS(ctx context.Context, osm *osDNSManager, run func(context.Context) error) error {
	osm.Set()
	defer osm.Restore()
	return run(ctx)
}
