#!/usr/bin/env bash
# LombokDNSProxy — Build .dmg installer for macOS
# Requires: create-dmg (brew install create-dmg), Wails build already done
# Usage: scripts/package_dmg.sh v1.0.0
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
set -euo pipefail

VERSION="${1:-v1.0.0}"
APP_NAME="LombokDNSProxy"
APP_PATH="src/gui/build/bin/${APP_NAME}.app"
OUT_DIR="dist"
DMG_NAME="${APP_NAME}-${VERSION}-macOS.dmg"

echo "▶ Building .dmg: ${DMG_NAME}"

# Verify app bundle exists
if [ ! -d "${APP_PATH}" ]; then
  echo "✗ App bundle not found at ${APP_PATH}. Run: task build:gui" >&2
  exit 1
fi

# Code sign (requires APPLE_SIGNING_IDENTITY env var)
if [ -n "${APPLE_SIGNING_IDENTITY:-}" ]; then
  echo "  Signing app bundle..."
  codesign --deep --force --verify --verbose \
    --sign "${APPLE_SIGNING_IDENTITY}" \
    --options runtime \
    --entitlements scripts/entitlements.plist \
    "${APP_PATH}"
else
  echo "  ⚠ Skipping code signing (APPLE_SIGNING_IDENTITY not set)"
fi

# Create DMG
mkdir -p "${OUT_DIR}"
create-dmg \
  --volname "${APP_NAME} ${VERSION}" \
  --volicon "assets/icons/icon.icns" \
  --window-pos 200 120 \
  --window-size 600 400 \
  --icon-size 100 \
  --icon "${APP_NAME}.app" 175 190 \
  --hide-extension "${APP_NAME}.app" \
  --app-drop-link 425 190 \
  --no-internet-enable \
  "${OUT_DIR}/${DMG_NAME}" \
  "${APP_PATH}"

# Notarise (requires APPLE_NOTARY_PROFILE env var)
if [ -n "${APPLE_NOTARY_PROFILE:-}" ]; then
  echo "  Notarising..."
  xcrun notarytool submit "${OUT_DIR}/${DMG_NAME}" \
    --keychain-profile "${APPLE_NOTARY_PROFILE}" \
    --wait
  xcrun stapler staple "${OUT_DIR}/${DMG_NAME}"
fi

echo "✓ Built: ${OUT_DIR}/${DMG_NAME}"
