#!/bin/sh
# Builds deck for the desktop into desktop/dist: deck.app for macOS (Apple silicon) and deck-setup.exe for Windows.
set -eu
cd "$(dirname "$0")"
rm -rf dist && mkdir -p dist/windows
(cd .. && GOOS=darwin GOARCH=arm64 go build -tags tailnet -o desktop/dist/deck-server ./cmd/deck)
(cd .. && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags tailnet -o desktop/dist/windows/deck-linux ./cmd/deck)

CGO_CFLAGS=-mmacosx-version-min=11.0 CGO_LDFLAGS=-mmacosx-version-min=11.0 go build -o dist/deck-launcher .
app=dist/deck.app/Contents
mkdir -p "$app/MacOS" "$app/Resources" dist/deck.iconset
cp dist/deck-launcher "$app/MacOS/deck"
cp dist/deck-server "$app/Resources/deck"
cp build/Info.plist "$app/Info.plist"
for s in 16 32 128 256 512; do
  sips -z $s $s icon.png --out dist/deck.iconset/icon_${s}x${s}.png >/dev/null
  sips -z $((s * 2)) $((s * 2)) icon.png --out dist/deck.iconset/icon_${s}x${s}@2x.png >/dev/null
done
iconutil -c icns dist/deck.iconset -o "$app/Resources/deck.icns"
codesign --force --deep --sign - dist/deck.app
rm -rf dist/deck.iconset dist/deck-launcher dist/deck-server

GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o dist/windows/deck.exe .
cp build/deck.nsi build/deck.ico dist/windows/
(cd dist/windows && makensis -V2 deck.nsi)
mv dist/windows/deck-setup.exe dist/
rm -rf dist/windows
ls -la dist
