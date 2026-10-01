#!/usr/bin/env bash
# LombokDNSProxy — Universal Installer
# curl -sSL https://install.lombokdns.dev | bash
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
set -euo pipefail

REPO="codinglombok/lombok-dns-proxy"
INSTALL_DIR="/usr/local/bin"
BIN_NAME="lombok-dns"
CONFIG_DIR="${HOME}/.config/lombok-dns"
SERVICE_NAME="lombok-dns"

# ─── Helpers ─────────────────────────────────────────────────────────────────

info()    { printf "\033[0;32m▶\033[0m %s\n" "$*"; }
warn()    { printf "\033[0;33m⚠\033[0m %s\n" "$*"; }
err()     { printf "\033[0;31m✗\033[0m %s\n" "$*" >&2; exit 1; }
success() { printf "\033[0;32m✓\033[0m %s\n" "$*"; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || err "Required command not found: $1"; }

# ─── Detect OS / arch ────────────────────────────────────────────────────────

detect_platform() {
  OS="$(uname -s)"
  ARCH="$(uname -m)"

  case "${OS}" in
    Linux)  OS_SLUG="linux" ;;
    Darwin) OS_SLUG="darwin" ;;
    *)      err "Unsupported OS: ${OS}. Install manually from https://github.com/${REPO}/releases" ;;
  esac

  case "${ARCH}" in
    x86_64|amd64) ARCH_SLUG="amd64" ;;
    aarch64|arm64) ARCH_SLUG="arm64" ;;
    *) err "Unsupported architecture: ${ARCH}" ;;
  esac

  BINARY_NAME="${BIN_NAME}-${OS_SLUG}-${ARCH_SLUG}"
  info "Detected platform: ${OS_SLUG}/${ARCH_SLUG}"
}

# ─── Fetch latest release version ────────────────────────────────────────────

get_latest_version() {
  need_cmd curl
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')
  [ -n "${VERSION}" ] || err "Could not determine latest release version"
  info "Latest version: ${VERSION}"
}

# ─── Download and verify ──────────────────────────────────────────────────────

download_binary() {
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
  DOWNLOAD_URL="${BASE_URL}/${BINARY_NAME}"
  CHECKSUM_URL="${BASE_URL}/checksums.txt"

  TMP_DIR=$(mktemp -d)
  trap 'rm -rf "${TMP_DIR}"' EXIT

  info "Downloading ${BINARY_NAME}..."
  curl -fsSL --progress-bar "${DOWNLOAD_URL}" -o "${TMP_DIR}/${BIN_NAME}"

  # Verify SHA-256 checksum
  info "Verifying checksum..."
  curl -fsSL "${CHECKSUM_URL}" -o "${TMP_DIR}/checksums.txt"
  EXPECTED=$(grep "${BINARY_NAME}" "${TMP_DIR}/checksums.txt" | awk '{print $1}')
  if [ -z "${EXPECTED}" ]; then
    warn "No checksum entry found for ${BINARY_NAME} — skipping verification"
  else
    if command -v sha256sum >/dev/null 2>&1; then
      GOT=$(sha256sum "${TMP_DIR}/${BIN_NAME}" | awk '{print $1}')
    else
      GOT=$(shasum -a 256 "${TMP_DIR}/${BIN_NAME}" | awk '{print $1}')
    fi
    [ "${GOT}" = "${EXPECTED}" ] || err "Checksum mismatch! Expected ${EXPECTED}, got ${GOT}"
    success "Checksum verified"
  fi

  chmod +x "${TMP_DIR}/${BIN_NAME}"
  echo "${TMP_DIR}/${BIN_NAME}"
}

# ─── Install binary ───────────────────────────────────────────────────────────

install_binary() {
  local tmp_bin="$1"

  if [ -w "${INSTALL_DIR}" ]; then
    mv "${tmp_bin}" "${INSTALL_DIR}/${BIN_NAME}"
  else
    info "Requesting sudo to install to ${INSTALL_DIR}..."
    sudo mv "${tmp_bin}" "${INSTALL_DIR}/${BIN_NAME}"
  fi
  success "Installed to ${INSTALL_DIR}/${BIN_NAME}"
}

# ─── Write default config ─────────────────────────────────────────────────────

write_config() {
  mkdir -p "${CONFIG_DIR}"
  if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
    cat > "${CONFIG_DIR}/config.yaml" << 'CONF'
# LombokDNSProxy — auto-generated config
# Edit to customise. Full reference: https://github.com/codinglombok/lombok-dns-proxy
dns:
  listen: "127.0.0.1"
  port: 53
  cache_size: 100000

racing:
  enabled: true
  max_upstreams: 4

filter:
  enabled: true

upstreams:
  - name: "Cloudflare DoH"
    address: "https://1.1.1.1/dns-query"
    protocol: "doh"
    enabled: true
  - name: "Google DoH"
    address: "https://8.8.8.8/dns-query"
    protocol: "doh"
    enabled: true
  - name: "Quad9 DoT"
    address: "tls://9.9.9.9:853"
    protocol: "dot"
    enabled: true

http_proxy:
  enabled: true
  port: 7788

socks5:
  enabled: true
  port: 7789

dashboard:
  enabled: true
  port: 7787
CONF
    success "Config written to ${CONFIG_DIR}/config.yaml"
  else
    info "Config already exists at ${CONFIG_DIR}/config.yaml — skipping"
  fi
}

# ─── Install systemd service (Linux only) ────────────────────────────────────

install_systemd() {
  [ "${OS_SLUG}" = "linux" ] || return 0
  command -v systemctl >/dev/null 2>&1 || return 0

  UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
  info "Installing systemd service..."

  sudo tee "${UNIT_PATH}" > /dev/null << UNIT
[Unit]
Description=LombokDNSProxy — Privacy-first DNS proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BIN_NAME} start --config ${CONFIG_DIR}/config.yaml
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

  sudo systemctl daemon-reload
  success "systemd unit installed: ${UNIT_PATH}"
  info "Run: sudo systemctl enable --now ${SERVICE_NAME}"
}

# ─── Install launchd plist (macOS only) ──────────────────────────────────────

install_launchd() {
  [ "${OS_SLUG}" = "darwin" ] || return 0
  PLIST="${HOME}/Library/LaunchAgents/dev.lombokdns.proxy.plist"
  info "Installing launchd agent..."

  cat > "${PLIST}" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.lombokdns.proxy</string>
  <key>ProgramArguments</key>
  <array>
    <string>${INSTALL_DIR}/${BIN_NAME}</string>
    <string>start</string>
    <string>--config</string>
    <string>${CONFIG_DIR}/config.yaml</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${HOME}/.config/lombok-dns/daemon.log</string>
  <key>StandardErrorPath</key>
  <string>${HOME}/.config/lombok-dns/daemon.log</string>
</dict>
</plist>
PLIST

  launchctl load "${PLIST}" 2>/dev/null || true
  success "launchd agent installed: ${PLIST}"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  printf "\n\033[1m🛡️  LombokDNSProxy Installer\033[0m\n"
  printf "   Part of the Lombok Ecosystem · Apache 2.0\n\n"

  need_cmd curl
  detect_platform
  get_latest_version

  TMP_BIN=$(download_binary)
  install_binary "${TMP_BIN}"
  write_config
  install_systemd
  install_launchd

  printf "\n"
  success "LombokDNSProxy ${VERSION} installed successfully!"
  printf "\n"
  printf "  Start now:    \033[1mlombok-dns start\033[0m\n"
  printf "  Dashboard:    \033[1mhttp://127.0.0.1:7787\033[0m\n"
  printf "  Benchmark:    \033[1mlombok-dns bench\033[0m\n"
  printf "  Docs:         \033[1mhttps://github.com/${REPO}\033[0m\n"
  printf "\n"
}

main "$@"
