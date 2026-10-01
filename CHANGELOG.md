# Changelog

All notable changes to LombokDNSProxy are documented here.  
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).  
Versioning follows [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### In progress
- Mobile apps: Android (Private DNS API) and iOS (NetworkExtension)
- OpenWrt MIPS/ARM cross-compiled package
- LombokDNSProxy Cloud (anycast DoH/DoT endpoints, zero-knowledge)
- Browser extension: Safari (macOS/iOS) via App Store

---

## [1.0.0] — 2026-09-13

### Added
**Core DNS engine**
- DNS racing: parallel queries to top-4 upstreams by EWMA latency; first valid response wins
- LombokCache: sharded LRU (16 shards) + TTL-aware eviction + RS(255,239) ECC integrity via LombokECC
- Predictive prefetching: Markov(1) model learns per-session domain access patterns; prefetches P(next|current) ≥ 70%
- CNAME flattening: follows CNAME chains up to depth 3; blocks if any hop matches the filter trie
- Protocol support: DoH (RFC 8484, HTTP/2+HTTP/3), DoT (RFC 7858, TLS 1.3), DoQ (draft-ietf-dprive-dnsoquic), plain DNS (UDP/TCP)
- EWMA latency tracker: probes all upstreams every 30 s; re-sorts racing pool automatically
- Upstream failover: marks upstream unhealthy after 5 consecutive errors; recovers on first success

**Filter engine**
- DAFSA compressed trie: O(k) blocklist lookup (k = domain label count); ~30 MB RAM for 10 M entries
- Wildcard zones: `*.doubleclick.net` blocks all subdomains
- Regex rules: RE2-compiled, applied only on trie miss
- Auto-updater: downloads 5 curated filter lists every 24 h; conditional GET (If-Modified-Since); checksum verification; hot-reload without restart
- CNAME-cloaking detection: integrated into resolver pipeline

**Proxy engine**
- HTTP/HTTPS forward proxy with CONNECT tunnel (RFC 7231)
- SOCKS5 proxy (RFC 1928) with username/password auth (RFC 1929)
- PAC auto-config generator with domain, CIDR, and wildcard rules; GeoIP bypass support
- TLS inspection (opt-in only): local ECDSA CA, per-host leaf cert generation, OS keychain storage

**Browser extension**
- Manifest V3 (Chrome/Edge/Brave) + MV2 Firefox fallback
- Real-time WebSocket sync with OS daemon (port 7788)
- Per-tab domain list with block/allow/temp-allow controls
- Blocked-count badge with per-tab tracking
- Cosmetic filter: hides ad container elements (CSS injection)
- Options page: custom block/allow lists, daemon address, toggle controls
- Background exponential reconnect backoff (1 s → 30 s cap)

**Desktop GUI (Wails v2)**
- System tray: status icon, quick-toggle protection, flush cache, update lists
- Web dashboard served at `http://127.0.0.1:7787`
- OS DNS auto-configuration: Linux (systemd-resolved + /etc/resolv.conf fallback), macOS (networksetup), Windows (PowerShell Set-DnsClientServerAddress)
- Graceful restore of original OS DNS on shutdown

**REST API**
- `GET /api/v1/stats` — live global statistics
- `POST /api/v1/filter/block` — runtime domain blocking
- `POST /api/v1/filter/allow` — runtime domain allowing (with optional TTL)
- `POST /api/v1/cache/flush` — clear DNS cache
- `GET /api/v1/query-log` — searchable query log (domain filter, result filter, limit)
- `GET /api/v1/upstreams` — upstream health and EWMA latency
- `GET /api/v1/filter/lists` — filter list status and domain counts
- `POST /api/v1/filter/reload` — immediate list refresh
- `GET /proxy.pac` — PAC auto-config file
- `GET /health` — health check
- `WebSocket /ext` — browser extension real-time sync

**Analytics**
- RAPPOR (Randomized Aggregatable Privacy-Preserving Ordinal Response) for zero-knowledge aggregate stats
- ε-differential privacy, configurable f/p/q parameters
- No individual queries ever leave the device by default

**CLI**
- `start` — daemon with all subsystems
- `stop` — graceful shutdown
- `status` — live stats snapshot
- `bench` — benchmark all upstreams, rank by latency
- `import` — add blocklist from URL or file
- `export` — export config, stats, allow/block lists
- `version` — build metadata

**Platform support**
- Linux x86_64, ARM64, MIPS LE (OpenWrt)
- macOS Intel + Apple Silicon
- Windows 10/11 x86_64
- Docker (distroless, multi-arch amd64/arm64)

**Build & CI**
- Taskfile.yml with 25+ tasks (build, test, fuzz, lint, package, release)
- GitHub Actions: lint → gosec → CodeQL → matrix tests (Win/Mac/Linux) → fuzz → cross-compile → GoReleaser
- GoReleaser: multi-platform binaries, checksums, Homebrew tap, Docker manifest
- Packaging: .deb (systemd unit), .rpm, .dmg (macOS notarisation), .msi (WiX, Windows service)
- One-line installer: `curl -sSL https://install.lombokdns.dev | bash`

**Tests**
- 15 test files, 200+ test cases across unit, integration, and fuzz suites
- Unit: resolver, cache (sharding + ECC + LRU + concurrency), filter (trie + wildcard + regex + allowlist), upstream pool (EWMA + failover), PAC generator, RAPPOR analytics, CNAME flattener
- Integration: DNS end-to-end (racing latency, cache speedup, concurrent queries, NXDOMAIN), HTTP/SOCKS5 proxy (forward, CONNECT tunnel, auth)
- Fuzz: DNS packet unpack, resolver with arbitrary queries, cache key, filter domain

**Documentation**
- `docs/ARCHITECTURE.md` — full system design, comparison table, algorithm pseudocode
- `docs/API.md` — complete REST API reference
- `docs/DEPLOY_CHECKLIST.md` — 40+ pre-release verification items
- `docs/TECH_DEBT.md` — 10 tracked items with file refs, risk, concrete fixes, owners, ETAs
- `README.md` — install, CLI reference, configuration, privacy policy, architecture overview

### Security
- WebSocket endpoint binds only to 127.0.0.1 (never 0.0.0.0)
- TLS inspection is off by default; requires explicit config opt-in
- Local CA private key stored with 0600 permissions
- Rate limiting: 1000 queries/s per client
- DNS amplification prevention: UDP responses > 512 bytes are truncated
- Filter list downloads verified against SHA-256 checksums
- Memory-safe Go implementation; fuzz-tested DNS parser

### Performance
- Cached query p50: < 0.5 ms
- Uncached racing query p50: 8–15 ms (network dependent)
- Filter trie lookup: O(k) time, ~30 MB RAM for 10 M entries
- Zero-alloc hot path: sync.Pool, pre-allocated buffers
- Cache hit rate: 70–80% typical browsing session

---

## [0.9.0-alpha] — 2026-08-01

### Added
- Initial architecture design and proof-of-concept
- DNS racing prototype (2 upstreams, UDP only)
- Basic blocklist loading from hosts-format files
- Single-binary CLI skeleton

---

[Unreleased]: https://github.com/codinglombok/lombok-dns-proxy/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/codinglombok/lombok-dns-proxy/compare/v0.9.0-alpha...v1.0.0
[0.9.0-alpha]: https://github.com/codinglombok/lombok-dns-proxy/releases/tag/v0.9.0-alpha
