// Package filter — LombokDNSProxy · List Updater
// Downloads, verifies, and hot-reloads blocklists on a configurable schedule.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package filter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ListSource describes one remote blocklist.
type ListSource struct {
	Name     string
	URL      string
	ChecksumURL string // optional .sha256 sidecar
	Enabled  bool
}

// Updater periodically downloads blocklists and reloads the filter Engine.
type Updater struct {
	engine   *Engine
	sources  []ListSource
	cacheDir string
	interval time.Duration
	log      *zap.Logger
	client   *http.Client
}

// NewUpdater creates an Updater that keeps engine's lists fresh.
func NewUpdater(engine *Engine, sources []ListSource, cacheDir string, interval time.Duration, log *zap.Logger) *Updater {
	return &Updater{
		engine:   engine,
		sources:  sources,
		cacheDir: cacheDir,
		interval: interval,
		log:      log,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Run starts the update loop. Performs an immediate update on start,
// then repeats on the configured interval. Blocks until ctx is cancelled.
func (u *Updater) Run(ctx context.Context) {
	u.log.Info("filter updater started", zap.Duration("interval", u.interval))
	u.update(ctx)

	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			u.update(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// update downloads all enabled sources and reloads the engine atomically.
func (u *Updater) update(ctx context.Context) {
	u.log.Info("updating filter lists")
	u.engine.Flush()

	var total int
	for _, src := range u.sources {
		if !src.Enabled {
			continue
		}
		n, err := u.loadSource(ctx, src)
		if err != nil {
			u.log.Warn("failed to load list", zap.String("name", src.Name), zap.Error(err))
			continue
		}
		total += n
		u.log.Info("loaded list", zap.String("name", src.Name), zap.Int("domains", n))
	}
	u.log.Info("filter reload complete", zap.Int("total_blocked_domains", total))
}

// loadSource downloads src, optionally verifies checksum, and feeds into engine.
// Returns number of domains loaded.
func (u *Updater) loadSource(ctx context.Context, src ListSource) (int, error) {
	path, err := u.fetchToCache(ctx, src)
	if err != nil {
		return 0, fmt.Errorf("fetch %s: %w", src.URL, err)
	}

	if src.ChecksumURL != "" {
		if err := u.verifyChecksum(ctx, path, src.ChecksumURL); err != nil {
			return 0, fmt.Errorf("checksum mismatch for %s: %w", src.Name, err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	before := u.engine.Size()
	if err := u.engine.LoadList(f); err != nil {
		return 0, err
	}
	return u.engine.Size() - before, nil
}

// fetchToCache downloads url to a local cache file. Returns the local path.
// Uses ETag/Last-Modified for conditional GETs when the cached file exists.
func (u *Updater) fetchToCache(ctx context.Context, src ListSource) (string, error) {
	if err := os.MkdirAll(u.cacheDir, 0o750); err != nil {
		return "", err
	}

	// Sanitise name for filename use
	safe := filepath.Base(src.Name)
	cachePath := filepath.Join(u.cacheDir, safe+".txt")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "LombokDNSProxy/1.0 (https://github.com/codinglombok/lombok-dns-proxy)")

	// Conditional GET using cached file mtime as If-Modified-Since
	if info, err := os.Stat(cachePath); err == nil {
		req.Header.Set("If-Modified-Since", info.ModTime().UTC().Format(http.TimeFormat))
	}

	resp, err := u.client.Do(req)
	if err != nil {
		// Fallback to cached file if network fails
		if _, statErr := os.Stat(cachePath); statErr == nil {
			u.log.Warn("network error, using cached list", zap.String("name", src.Name))
			return cachePath, nil
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return cachePath, nil // cache is still valid
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, src.URL)
	}

	tmp, err := os.CreateTemp(u.cacheDir, "*.tmp")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()

	return cachePath, os.Rename(tmp.Name(), cachePath)
}

// verifyChecksum downloads the .sha256 sidecar and compares against local file.
func (u *Updater) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(string(b))

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))

	if got != expected {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, expected)
	}
	return nil
}
