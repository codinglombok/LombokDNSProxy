# Contributing to LombokDNSProxy

Thank you for contributing to LombokDNSProxy!  
Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok

---

## Getting started

### Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | ≥ 1.22 | https://go.dev/dl |
| Node.js | ≥ 20 | https://nodejs.org |
| Task | latest | `go install github.com/go-task/task/v3/cmd/task@latest` |
| Air | latest | `go install github.com/air-verse/air@latest` |
| golangci-lint | latest | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` |
| Wails | v2 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |

Install all at once:
```bash
task install:tools
```

### First-time setup

```bash
git clone https://github.com/codinglombok/lombok-dns-proxy
cd lombok-dns-proxy

# Install Go deps
go mod download

# Install browser extension deps
npm install

# Update + compile filter lists
task lists:update
task lists:compile

# Run the full CI pipeline locally
task ci
```

---

## Development workflow

### Running the daemon in dev mode
```bash
task dev       # starts on port 5353 (unprivileged), hot-reload via Air
```

### Running the browser extension
```bash
task dev:ext   # watch-builds extension TypeScript
```
Then load `src/browser-ext/dist/` as an unpacked extension in Chrome.

### Running tests
```bash
task test           # all unit tests with race detector
task test:short     # fast subset (no benchmarks)
task test:cover     # coverage report → coverage.html
task bench          # all benchmarks
task fuzz:filter    # fuzz filter engine for 60s
task fuzz:parser    # fuzz DNS parser for 60s
```

### Integration tests (requires network)
```bash
go test -tags integration -v ./tests/integration/...
```

---

## Code standards

### Go
- `gofmt` / `goimports` formatting (enforced by CI)
- All exported symbols must have doc comments
- Errors must be handled or explicitly ignored with `//nolint:errcheck` + reason
- New packages need a package-level doc comment
- DNS hot paths must remain zero-allocation (use `sync.Pool`, pre-allocated buffers)
- No `fmt.Println` in library code — use `go.uber.org/zap`

### TypeScript (browser extension)
- Strict mode (`strict: true`, `noUncheckedIndexedAccess: true`)
- No `any` types without a comment explaining why
- All async functions must handle errors

### Testing requirements
- New features need ≥ 1 happy-path test and ≥ 1 error/edge test
- Bug fixes need a regression test that fails before the fix
- Coverage on `src/core/`, `src/filter/`, `src/cache/` must not drop below 80%
- Fuzz targets must not panic on any input

---

## Pull request process

1. **Fork** the repository and create a feature branch: `feature/my-feature`
2. **Write code** following the standards above
3. **Run the full CI pipeline**: `task ci` — all checks must pass
4. **Update docs**: update `docs/ARCHITECTURE.md` and `docs/API.md` if behaviour changes
5. **Update CHANGELOG.md** under `[Unreleased]`
6. **Open a PR** against `main` with a clear description of what and why

### PR checklist
- [ ] `task ci` passes (lint + vet + gosec + tests + cover)
- [ ] No new `TODO: security` or `FIXME: data-race` comments without a TECH_DEBT.md entry
- [ ] Breaking changes documented in CHANGELOG.md and PR description
- [ ] New public APIs have doc comments
- [ ] No test coverage decrease on core packages

---

## Reporting bugs

Use GitHub Issues with the `bug` label. Include:
- LombokDNSProxy version (`lombok-dns version`)
- OS and architecture
- Steps to reproduce
- Expected vs. actual behaviour
- Relevant logs (with `--log-level debug`)

---

## Security vulnerabilities

**Do not open a public GitHub Issue for security vulnerabilities.**

Email `security@codinglombok.dev` with:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (optional)

We aim to respond within 48 hours and release a patch within 7 days.

---

## Architecture decisions

Major design decisions are documented in `docs/ARCHITECTURE.md`.  
New ADRs (Architecture Decision Records) should be added as pull requests to that file.

When making a significant design choice (new protocol, new caching strategy, etc.):
1. Open a GitHub Discussion first to get community input
2. Document the decision, alternatives considered, and rationale in ARCHITECTURE.md
3. Reference the PR that implements it

---

## Tech debt

Known technical debt is tracked in `docs/TECH_DEBT.md`.  
When introducing new debt, add an entry with:
- Priority (1/2/3)
- File reference and line
- Risk assessment
- Concrete fix path
- Owner and ETA

---

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.  
See [LICENSE](LICENSE) for details.
