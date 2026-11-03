#!/bin/sh
# Package the three desktop platforms. It runs on any host: the binaries are
# cross-compiled, and the bundle layout is just files, so nothing here needs
# macOS tooling except signing, which is deliberately left to the release owner.
set -eu
cd "$(dirname "$0")/.."
VERSION="$(cat VERSION)"
DIST="dist"
rm -rf "$DIST"
mkdir -p "$DIST"

build() { # os arch output
	CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -tags mygo_noinspector \
		-ldflags "-s -w -X main.version=$VERSION" -o "$3" ./cmd/zeokeel
}

echo "building $VERSION"
build linux amd64 "$DIST/zeokeel-linux-amd64"
build darwin arm64 "$DIST/zeokeel-darwin-arm64"
build darwin amd64 "$DIST/zeokeel-darwin-amd64"
build windows amd64 "$DIST/zeokeel-windows-amd64.exe"

# Linux: a tarball with the desktop entry, so a file manager can install it.
mkdir -p "$DIST/linux"
cp "$DIST/zeokeel-linux-amd64" "$DIST/linux/zeokeel"
cp packaging/linux/zeokeel.desktop "$DIST/linux/"
cp packaging/icons/zeokeel-256.png "$DIST/linux/zeokeel.png"
cp CHANGELOG.md "$DIST/linux/"
tar -C "$DIST" -czf "$DIST/zeokeel-$VERSION-linux-x86_64.tar.gz" linux

# macOS: a real .app bundle. Signing and notarization are a separate, credentialed
# step; an unsigned bundle still opens with right-click → Open.
APP="$DIST/darwin/ZeoKeel.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
sed "s/__VERSION__/$VERSION/g" packaging/darwin/Info.plist > "$APP/Contents/Info.plist"
cp "$DIST/zeokeel-darwin-arm64" "$APP/Contents/MacOS/zeokeel"
chmod +x "$APP/Contents/MacOS/zeokeel"
cp CHANGELOG.md "$APP/Contents/Resources/"
cp README.md "$APP/Contents/Resources/"
cp packaging/icons/zeokeel.icns "$APP/Contents/Resources/zeokeel.icns"
# The bundle is unsigned; saying so next to the binary is better than a support
# thread about Gatekeeper.
printf 'This build is unsigned. First launch: right-click the app, then Open.\n' \
	> "$APP/Contents/Resources/UNSIGNED.txt"
python3 scripts/zipdir.py "$DIST/darwin" "$DIST/zeokeel-$VERSION-macos-arm64.zip" "ZeoKeel.app"

# Windows: zip with the exe and the changelog.
mkdir -p "$DIST/windows"
cp "$DIST/zeokeel-windows-amd64.exe" "$DIST/windows/zeokeel.exe"
cp packaging/icons/zeokeel.ico "$DIST/windows/"
cp CHANGELOG.md "$DIST/windows/"
cp packaging/windows/install.ps1 "$DIST/windows/"
cp README.md "$DIST/windows/"
python3 scripts/zipdir.py "$DIST" "$DIST/zeokeel-$VERSION-windows-x86_64.zip" "windows"

echo
echo "artifacts:"
ls -lh "$DIST"/*.tar.gz "$DIST"/*.zip 2>/dev/null
