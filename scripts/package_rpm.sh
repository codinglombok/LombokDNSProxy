#!/usr/bin/env bash
# LombokDNSProxy — Build .rpm package for RHEL/Fedora/CentOS
# Usage: scripts/package_rpm.sh v1.0.0
# Requires: rpmbuild (dnf install rpm-build)
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
set -euo pipefail

VERSION="${1:-v1.0.0}"
VER="${VERSION#v}"
ARCH="${2:-x86_64}"
BINARY="dist/lombok-dns-proxy-linux-amd64"

echo "▶ Building .rpm: lombok-dns-proxy-${VER}-1.${ARCH}.rpm"

# Set up rpmbuild tree
RPM_ROOT="${HOME}/rpmbuild"
mkdir -p "${RPM_ROOT}"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

# Create source tarball
SRCDIR="lombok-dns-proxy-${VER}"
mkdir -p "/tmp/${SRCDIR}/usr/bin"
mkdir -p "/tmp/${SRCDIR}/etc/lombok-dns"
mkdir -p "/tmp/${SRCDIR}/lib/systemd/system"

install -m 0755 "${BINARY}" "/tmp/${SRCDIR}/usr/bin/lombok-dns"
install -m 0644 configs/default.yaml "/tmp/${SRCDIR}/etc/lombok-dns/config.yaml"

cat > "/tmp/${SRCDIR}/lib/systemd/system/lombok-dns.service" << 'UNIT'
[Unit]
Description=LombokDNSProxy — Privacy-first DNS proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lombok-dns
ExecStart=/usr/bin/lombok-dns start --config /etc/lombok-dns/config.yaml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
UNIT

tar -czf "${RPM_ROOT}/SOURCES/${SRCDIR}.tar.gz" -C /tmp "${SRCDIR}"
rm -rf "/tmp/${SRCDIR}"

# Write spec file
cat > "${RPM_ROOT}/SPECS/lombok-dns-proxy.spec" << SPEC
Name:           lombok-dns-proxy
Version:        ${VER}
Release:        1%{?dist}
Summary:        Privacy-first DNS proxy, ad blocker, and internet speed optimizer
License:        Apache-2.0
URL:            https://github.com/codinglombok/lombok-dns-proxy
Source0:        %{name}-%{version}.tar.gz
BuildArch:      ${ARCH}

%description
LombokDNSProxy is a high-performance DNS proxy with racing,
predictive prefetch, ECC-verified cache, DoH/DoT/DoQ support,
HTTP/SOCKS5 proxy, and a browser extension.
Part of the Lombok Ecosystem.

%prep
%setup -q

%install
mkdir -p %{buildroot}
cp -r usr %{buildroot}/
cp -r etc %{buildroot}/
cp -r lib %{buildroot}/

%pre
getent group lombok-dns > /dev/null || groupadd --system lombok-dns
getent passwd lombok-dns > /dev/null || useradd --system --gid lombok-dns \
  --home /var/lib/lombok-dns --shell /sbin/nologin lombok-dns

%post
systemctl daemon-reload
echo "Start with: sudo systemctl enable --now lombok-dns"

%preun
systemctl stop lombok-dns 2>/dev/null || true
systemctl disable lombok-dns 2>/dev/null || true

%files
/usr/bin/lombok-dns
%config(noreplace) /etc/lombok-dns/config.yaml
/lib/systemd/system/lombok-dns.service

%changelog
* $(date '+%a %b %d %Y') Lombok Ecosystem <hello@codinglombok.dev> - ${VER}-1
- Initial package
SPEC

# Build
rpmbuild -bb "${RPM_ROOT}/SPECS/lombok-dns-proxy.spec"

RPM_FILE=$(find "${RPM_ROOT}/RPMS" -name "*.rpm" | head -1)
cp "${RPM_FILE}" "dist/"
echo "✓ Built: dist/$(basename ${RPM_FILE})"
echo "  Install: sudo rpm -i dist/$(basename ${RPM_FILE})"
