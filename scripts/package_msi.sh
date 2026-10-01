#!/usr/bin/env bash
# LombokDNSProxy — Build .msi installer for Windows (using WiX Toolset v4)
# Usage: scripts/package_msi.sh v1.0.0
# Requires: wix (dotnet tool install --global wix)
# Run on Windows or via cross-compile with wine
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
set -euo pipefail

VERSION="${1:-v1.0.0}"
VER="${VERSION#v}"
BINARY="dist/lombok-dns-proxy-windows-amd64.exe"

echo "▶ Building .msi: LombokDNSProxy-${VER}-windows-amd64.msi"

MSI_DIR="dist/msi"
mkdir -p "${MSI_DIR}"

# Copy binary
install -m 0755 "${BINARY}" "${MSI_DIR}/lombok-dns.exe"
install -m 0644 configs/default.yaml "${MSI_DIR}/config.yaml"
install -m 0644 LICENSE "${MSI_DIR}/LICENSE.txt"

# Generate WiX source
cat > "${MSI_DIR}/product.wxs" << WXS
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
  <Package Name="LombokDNSProxy"
           Version="${VER}.0"
           Manufacturer="Lombok Ecosystem"
           UpgradeCode="A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
           Language="1033"
           Codepage="1252">

    <MajorUpgrade DowngradeErrorMessage="A newer version is already installed." />
    <MediaTemplate EmbedCab="yes" />

    <Icon Id="icon.ico" SourceFile="assets/icons/icon.ico" />
    <Property Id="ARPPRODUCTICON" Value="icon.ico" />
    <Property Id="ARPHELPLINK" Value="https://github.com/codinglombok/lombok-dns-proxy" />

    <Feature Id="ProductFeature" Title="LombokDNSProxy" Level="1">
      <ComponentGroupRef Id="ProductComponents" />
      <ComponentGroupRef Id="ServiceComponents" />
    </Feature>

    <!-- UI -->
    <WixVariable Id="WixUILicenseRtf" Value="LICENSE.rtf" />
    <UIRef Id="WixUI_Minimal" />

  </Package>

  <Fragment>
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLFOLDER" Name="LombokDNSProxy" />
      </Directory>
      <Directory Id="CommonAppDataFolder">
        <Directory Id="CONFIGFOLDER" Name="LombokDNSProxy" />
      </Directory>
    </Directory>
  </Fragment>

  <Fragment>
    <ComponentGroup Id="ProductComponents" Directory="INSTALLFOLDER">
      <Component Id="BinaryComponent" Guid="B1B2B3B4-B5B6-B7B8-B9BA-BBBCBDBEBFC0">
        <File Id="MainExecutable" Source="lombok-dns.exe" KeyPath="yes">
          <Shortcut Id="startmenu" Directory="ProgramMenuFolder"
                    Name="LombokDNSProxy" WorkingDirectory="INSTALLFOLDER"
                    Advertise="yes" />
        </File>
      </Component>
    </ComponentGroup>

    <ComponentGroup Id="ServiceComponents" Directory="CONFIGFOLDER">
      <Component Id="ConfigComponent" Guid="C1C2C3C4-C5C6-C7C8-C9CA-CBCCCDCECFD0">
        <File Id="ConfigFile" Source="config.yaml" KeyPath="yes" />
        <ServiceInstall Id="LombokDNSService"
                        Name="LombokDNSProxy"
                        DisplayName="LombokDNSProxy DNS Service"
                        Description="Privacy-first DNS proxy — Lombok Ecosystem"
                        Type="ownProcess"
                        Start="auto"
                        ErrorControl="normal"
                        Arguments="start --config &quot;[CONFIGFOLDER]config.yaml&quot;" />
        <ServiceControl Id="StartService" Name="LombokDNSProxy"
                        Start="install" Stop="both" Remove="uninstall" Wait="yes" />
      </Component>
    </ComponentGroup>
  </Fragment>
</Wix>
WXS

# Build MSI
wix build "${MSI_DIR}/product.wxs" \
  -o "dist/LombokDNSProxy-${VER}-windows-amd64.msi" \
  -b "${MSI_DIR}"

echo "✓ Built: dist/LombokDNSProxy-${VER}-windows-amd64.msi"
