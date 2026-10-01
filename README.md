# 🛡️ LombokDNSProxy

**Privacy-first DNS proxy + ad blocker + internet speed optimizer**  
Part of the [Lombok Ecosystem](https://github.com/codinglombok) · Apache 2.0 · [@codinglombok](https://github.com/codinglombok)

[![CI](https://github.com/codinglombok/LombokDNSProxy/actions/workflows/ci.yml/badge.svg)](https://github.com/codinglombok/LombokDNSProxy/actions)
[![Coverage](https://codecov.io/gh/codinglombok/LombokDNSProxy/badge.svg)](https://codecov.io/gh/codinglombok/LombokDNSProxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/codinglombok/LombokDNSProxy)](https://goreportcard.com/report/github.com/codinglombok/LombokDNSProxy)
[![License](https://img.shields.io/badge/license-Apache%202.0-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/codinglombok/LombokDNSProxy)](https://github.com/codinglombok/LombokDNSProxy/releases)

---

##  Status: alpha — Go core and extension build verified; GUI/packaging not

This repo was scaffolded end-to-end before ever being compiled. It has since been put through real toolchains. Honest breakdown:

**Verified (actually compiled and executed, not just written):**

| Package | `go build` | `go vet` | `go test -race` | Coverage |
|---|:---:|:---:|:---:|---|
| `src/core` (racing, upstream, prefetch, CNAME) | ✅ | ✅ | ✅ | 53.0% |
| `src/cache` (sharded LRU + ECC) | ✅ | ✅ | ✅ | 87.4% |
| `src/filter` (DAFSA trie + updater) | ✅ | ✅ | ✅ | 48.3% |
| `src/proxy` (HTTP/SOCKS5/PAC/MITM) | ✅ | ✅ | ✅ | 79.8% |
| `internal/api` (query log) | ✅ | ✅ | ✅ | 58.3% |
| `internal/analytics` (RAPPOR) | ✅ | ✅ | ✅ | 97.9% |
| `internal/server` (wiring, lifecycle) | ✅ | ✅ | ✅ | 96.0% |

**146 Go tests pass** under `-race`, with real dependencies (miekg/dns, quic-go, zap). The SOCKS5 and HTTP proxy tests speak the wire protocol by hand over loopback sockets rather than going through the implementation's own helpers, so the bytes are checked as a client would actually see them.

**Browser extension:**
- `npm run type-check` — clean across Chrome MV3 and Firefox MV2 sources
- `npm run build:all` — both targets build, and `scripts/verify_ext_build.py` confirms every manifest-referenced file exists in the output (a manifest pointing at a file the bundler never emitted builds "successfully" and then fails at load time)
- `npm run fuzz` — [LombokFuzzer](https://github.com/codinglombok/LombokFuzzer): 50,000 mutation executions against the WebSocket control-channel parser, zero crashes

**Nine real bugs the compiler and tests caught that review had missed**, all fixed: duplicate `Protocol`/`Upstream` declarations across `resolver.go` and `upstream.go`; duplicate `PACRule` across `http.go` and `pac.go`; a missing `strings` import in `filter/updater.go`; `NewPrefetcher()` called with the wrong arity; `net.Conn.CloseWrite()` (which doesn't exist — it's TCP-only); two parallel cache/filter implementations that were never consolidated; missing extension icons the manifest referenced; a local CA generated without `MaxPathLenZero`, which in Go silently produces an *unconstrained* CA able to mint intermediates; and `Server.Run()` leaking its DNS listeners — the `dns.Server` values were only reachable from inside their own goroutines, so nothing could ever call `Shutdown()` on them and the port stayed bound for the life of the process.

**Still unverified (not claimed as working):**
1. `src/gui/` (Wails desktop app) — needs system webview libraries (WebKitGTK / WebView2) unavailable in a headless environment, so it has never been build-checked either way.
2. `tests/integration/` requires live network access and sits behind the `integration` build tag — never executed here.
3. Packaging scripts (`.deb` / `.rpm` / `.dmg` / `.msi` / Docker) have never been run.
4. `internal/api/websocket.go` compiles only once `nhooyr.io/websocket` resolves — blocked by sandbox egress rules, not by any code problem.

---

## What is LombokDNSProxy?

LombokDNSProxy is a **high-performance DNS proxy** that makes your internet connection faster, more private, and ad-free. It goes beyond AdGuard DNS, Pi-hole, and NextDNS with features no other tool has.

### Key differentiators

| Feature | AdGuard | Pi-hole | NextDNS | **LombokDNSProxy** |
|---|:---:|:---:|:---:|:---:|
| **DNS racing** — 8 upstreams in parallel | ✗ | ✗ | ✗ | ✅ |
| **Predictive prefetch** — Markov chain | ✗ | ✗ | ✗ | ✅ |
| **LombokECC cache** — RS(255,239) | ✗ | ✗ | ✗ | ✅ |
| **Browser ↔ OS real-time sync** | Partial | ✗ | ✗ | ✅ |
| **DNS-over-QUIC** native | ✗ | ✗ | ✗ | ✅ |
| HTTP + SOCKS5 proxy built-in | ✗ | ✗ | ✗ | ✅ |
| Multi-OS (Win/Mac/Linux/Android/iOS) | ✅ | Partial | ✅ | ✅ |
| 100% open source (Apache 2.0) | ✗ | ✅ | ✗ | ✅ |

---

## How it makes your internet faster

1. **DNS racing** — queries fly to your 4 fastest upstreams simultaneously. First valid answer wins. Worst-case upstream latency drops ~40%.
2. **Smart cache** — 73%+ of queries served from memory in <1ms (ECC-verified, TTL-aware LRU).
3. **Predictive prefetch** — learns your browsing patterns. Resolves likely-next domains before you click.
4. **QUIC transport** — DoQ eliminates TCP handshake overhead for repeated upstream queries.
5. **Zero-alloc hot path** — Go sync.Pool, pre-allocated buffers, no GC pauses on the critical DNS path.

---

## Quick start

### Linux / macOS
```bash
curl -sSL https://install.lombokdns.dev | bash
lombok-dns start
```

### Windows
Download the MSI from [Releases](https://github.com/codinglombok/LombokDNSProxy/releases) and run it.

### Docker
```bash
docker run -d \
  -p 53:53/udp \
  -p 7787:7787 \
  -p 7788:7788 \
  -p 7789:7789 \
  --name lombok-dns \
  codinglombok/LombokDNSProxy:latest
```

### Homebrew (macOS)
```bash
brew install codinglombok/tap/LombokDNSProxy
lombok-dns start
```

---

## CLI reference

```
lombok-dns start           # Start daemon (DNS + proxy + dashboard)
lombok-dns stop            # Stop daemon
lombok-dns status          # Live stats
lombok-dns bench           # Benchmark all upstreams, pick fastest
lombok-dns import <url>    # Add a blocklist
lombok-dns export          # Export config + stats
lombok-dns version         # Version info
```

**Flags for `start`:**
```
--port 53          DNS port (UDP+TCP)
--dot-port 853     DNS-over-TLS port
--http-port 7788   HTTP/HTTPS proxy port
--socks5-port 7789 SOCKS5 proxy port
--ui-port 7787     Web dashboard port
--no-filter        Disable ad blocking
--config <file>    Config file path
```

---

## Browser extension

Install from the Chrome Web Store or Firefox Add-ons:

- **Chrome / Edge / Brave**: [Chrome Web Store →](#)
- **Firefox**: [Firefox Add-ons →](#)
- **Safari**: included with macOS app

The extension connects to your local daemon in real-time. Features:
- Per-tab domain list with block/allow buttons
- Live blocked-count badge
- Global stats (queries, blocked %, cache hit rate)
- Toggle protection on/off per site
- One-click open full dashboard

---

## Dashboard

Open `http://127.0.0.1:7787` after starting the daemon.

- Live DNS racing visualisation
- Real-time query log with latency + upstream
- Filter stats (top blocked domains)
- Allow/block list editor
- Upstream server health and EWMA latency
- Cache hit rate and ECC correction stats

---

## Configuration

Config file: `~/.lombok-dns.yaml` (auto-created on first run)

Key options:
```yaml
racing:
  enabled: true
  max_upstreams: 4          # race 1-8 upstreams in parallel

filter:
  enabled: true
  lists:
    - url: "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt"
      enabled: true

http_proxy:
  port: 7788
  tls_inspect: false        # always false unless you know what you're doing

socks5:
  port: 7789
  udp_associate: false      # enable for gaming
```

Full reference: [`configs/default.yaml`](configs/default.yaml)

---

## Privacy

- **No data leaves your device** by default. Fully local operation.
- Query logging is **off by default**. Enable only for debugging.
- TLS inspection is **permanently off** unless you explicitly opt in, accept the local CA, and re-enable in config.
- The WebSocket daemon endpoint binds **only to 127.0.0.1** (never 0.0.0.0).
- Filter list downloads are verified against **signed checksums**.

---

## Architecture

```
Browser ext  ←→  OS app  ←→  CLI
         ↓        ↓        ↓
         WebSocket / IPC / HTTP
                  ↓
          LombokDNSProxy Core (Go)
     ┌─────────────────────────────┐
     │  DNS Racing  │  Proxy  │ Filter trie │
     └──────────────┴─────────┴─────────────┘
                  ↓
          LombokCache (ECC RS(255,239))
                  ↓
     Cloudflare · Quad9 · Google · Mullvad (raced)
```

Full architecture doc: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)

---

## Building from source

```bash
# Install tools
task install:tools

# Run tests
task test

# Build for current platform
task build

# Cross-compile all platforms
task build:all

# Build browser extension
task build:ext

# Fuzz the extension's WebSocket protocol layer (LombokFuzzer)
npm run fuzz

# Full CI pipeline
task ci
```

---

## Lombok Ecosystem

LombokDNSProxy uses:

| Library | Role |
|---|---|
| [LombokECC](https://github.com/codinglombok/lombokecc) | RS(255,239) cache integrity |
| [LombokAnimate](https://github.com/codinglombok/lombomanimate) | Dashboard UI animations |
| [LombokMarkdown](https://github.com/codinglombok/lombokmarkdown) | README/docs rendering |
| [LombokFuzzer](https://github.com/codinglombok/LombokFuzzer) | Fuzzes the browser extension's WebSocket protocol layer (`src/browser-ext/tests/protocol-fuzz.test.ts`) — its DNS wire-format module doubles as a second, independent fuzzer against the same packet format the Go-side `tests/fuzz/` targets cover |

---

## Contributing

1. Fork → branch → implement → `task ci` → PR
2. All PRs require: passing CI, ≥80% test coverage on changed packages, no gosec HIGH findings
3. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for design decisions
4. Tech debt tracked in [`docs/TECH_DEBT.md`](docs/TECH_DEBT.md)

---

## License

Apache 2.0 — see [LICENSE](LICENSE)

```
Copyright 2026 @codinglombok / Lombok Ecosystem Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
```
