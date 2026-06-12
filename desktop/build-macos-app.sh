#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="DevX"
APP="$ROOT/build/${APP_NAME}.app"
BIN="$APP/Contents/MacOS/devx-desktop"
RES="$APP/Contents/Resources"
ICON_SRC="$ROOT/build/appicon.png"
ICONSET="$ROOT/build/appicon.iconset"
ICNS="$RES/appicon.icns"

if [[ ! -f "$ICON_SRC" ]]; then
  echo "missing icon source: $ICON_SRC" >&2
  exit 1
fi

rm -rf "$APP" "$ICONSET"
mkdir -p "$APP/Contents/MacOS" "$RES" "$ICONSET"

# Build the Wails binary. The tags match the manual spike command.
go build -tags desktop,production -o "$BIN" .

# Convert the existing PWA icon into a proper macOS .icns bundle. macOS ships
# sips/iconutil, so no extra dependency is required.
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$ICON_SRC" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
  dbl=$((size * 2))
  sips -z "$dbl" "$dbl" "$ICON_SRC" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$ICNS"
rm -rf "$ICONSET"

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleExecutable</key><string>devx-desktop</string>
  <key>CFBundleIconFile</key><string>appicon</string>
  <key>CFBundleIdentifier</key><string>works.earendil.devx.desktop</string>
  <key>CFBundleName</key><string>DevX</string>
  <key>CFBundleDisplayName</key><string>DevX</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.0.0-spike</string>
  <key>CFBundleVersion</key><string>0</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

chmod +x "$BIN"

echo "Built $APP"
echo "Run: open '$APP'"
