#!/usr/bin/env bash
# Builds dist/devknife.app — a double-clickable macOS app. The same binary
# is also the full CLI: ln -s /Applications/devknife.app/Contents/MacOS/devknife /usr/local/bin/devknife
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0}"
APP="dist/devknife.app"
export CGO_ENABLED=1
export CGO_CXXFLAGS="${CGO_CXXFLAGS:--Wno-deprecated-literal-operator}"
ARCHS="${ARCHS:-$(go env GOARCH)}" # set ARCHS="arm64 amd64" for a universal app

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" build

LDFLAGS="-s -w -X devknife/internal/cli.Version=$VERSION -X devknife/internal/cli.AppMode=1"
bins=()
for arch in $ARCHS; do
  echo "→ building darwin/$arch"
  GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "build/devknife-darwin-$arch" ./cmd/devknife
  bins+=("build/devknife-darwin-$arch")
done
if [ ${#bins[@]} -gt 1 ]; then
  lipo -create -output "$APP/Contents/MacOS/devknife" "${bins[@]}"
else
  cp "${bins[0]}" "$APP/Contents/MacOS/devknife"
fi

echo "→ icon"
go run ./scripts/mkicon -out build/icon >/dev/null
iconutil -c icns build/icon/devknife.iconset -o "$APP/Contents/Resources/devknife.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>devknife</string>
  <key>CFBundleDisplayName</key><string>devknife</string>
  <key>CFBundleIdentifier</key><string>dev.devknife.app</string>
  <key>CFBundleExecutable</key><string>devknife</string>
  <key>CFBundleIconFile</key><string>devknife</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSSupportsAutomaticGraphicsSwitching</key><true/>
</dict>
</plist>
PLIST

echo "→ ad-hoc signing"
codesign --force --deep --sign - "$APP" >/dev/null
echo "✓ $APP ($(du -sh "$APP" | cut -f1))"
