#!/usr/bin/env bash
# LombokDNSProxy — Build .deb package for Debian/Ubuntu
# Usage: scripts/package_deb.sh v1.0.0
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
set -euo pipefail

VERSION="${1:-v1.0.0}"
ARCH="${2:-amd64}"
PKG="lombok-dns-proxy_${VERSION#v}_${ARCH}"
BINARY="dist/lombok-dns-proxy-linux-${ARCH}"

echo "▶ Building .deb: ${PKG}.deb"

# Create debian package tree
rm -rf "dist/deb/${PKG}"
mkdir -p "dist/deb/${PKG}/DEBIAN"
mkdir -p "dist/deb/${PKG}/usr/bin"
mkdir -p "dist/deb/${PKG}/etc/lombok-dns"
mkdir -p "dist/deb/${PKG}/lib/systemd/system"
mkdir -p "dist/deb/${PKG}/usr/share/doc/lombok-dns-proxy"

# Binary
install -m 0755 "${BINARY}" "dist/deb/${PKG}/usr/bin/lombok-dns"

# Config
install -m 0644 configs/default.yaml "dist/deb/${PKG}/etc/lombok-dns/config.yaml"

# Systemd unit
cat > "dist/deb/${PKG}/lib/systemd/system/lombok-dns.service" << 'UNIT'
[Unit]
Description=LombokDNSProxy — Privacy-first DNS proxy
Documentation=https://github.com/codinglombok/lombok-dns-proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lombok-dns
Group=lombok-dns
ExecStart=/usr/bin/lombok-dns start --config /etc/lombok-dns/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/lombok-dns /var/log/lombok-dns

[Install]
WantedBy=multi-user.target
UNIT

# DEBIAN/control
cat > "dist/deb/${PKG}/DEBIAN/control" << CTRL
Package: lombok-dns-proxy
Version: ${VERSION#v}
Architecture: ${ARCH}
Maintainer: Lombok Ecosystem <hello@codinglombok.dev>
Depends: libc6
Recommends: systemd
Section: net
Priority: optional
Homepage: https://github.com/codinglombok/lombok-dns-proxy
Description: Privacy-first DNS proxy + ad blocker
 LombokDNSProxy is a high-performance DNS proxy with racing,
 predictive prefetch, ECC-verified cache, and multi-protocol support
 (DoH, DoT, DoQ). Includes HTTP/SOCKS5 proxy and browser extension.
 Part of the Lombok Ecosystem.
CTRL

# DEBIAN/postinst
cat > "dist/deb/${PKG}/DEBIAN/postinst" << 'POST'
#!/bin/sh
set -e
getent group lombok-dns > /dev/null || groupadd --system lombok-dns
getent passwd lombok-dns > /dev/null || useradd --system --gid lombok-dns \
  --home /var/lib/lombok-dns --shell /usr/sbin/nologin lombok-dns
mkdir -p /var/lib/lombok-dns /var/log/lombok-dns
chown lombok-dns:lombok-dns /var/lib/lombok-dns /var/log/lombok-dns
systemctl daemon-reload
echo "✓ LombokDNSProxy installed. Start with: sudo systemctl enable --now lombok-dns"
POST
chmod 0755 "dist/deb/${PKG}/DEBIAN/postinst"

# DEBIAN/prerm
cat > "dist/deb/${PKG}/DEBIAN/prerm" << 'PRERM'
#!/bin/sh
set -e
systemctl stop lombok-dns 2>/dev/null || true
systemctl disable lombok-dns 2>/dev/null || true
PRERM
chmod 0755 "dist/deb/${PKG}/DEBIAN/prerm"

# Copyright
install -m 0644 LICENSE "dist/deb/${PKG}/usr/share/doc/lombok-dns-proxy/copyright"

# Build .deb
dpkg-deb --build "dist/deb/${PKG}" "dist/${PKG}.deb"
echo "✓ Built: dist/${PKG}.deb"
echo "  Install: sudo dpkg -i dist/${PKG}.deb"
