#!/bin/sh
# Builds deck for the desktop into desktop/dist: deck.app for macOS (Apple silicon) with its zip, and deck-setup.exe
# for Windows; "mac" or "windows" builds one. VERSION and COMMIT, when set, make it that release.
set -eu
cd "$(dirname "$0")"
what=${1:-all}
release="-X deck2/internal/release.Version=${VERSION:-dev} -X deck2/internal/release.Commit=${COMMIT:-}"
rm -rf dist && mkdir -p dist

if [ "$what" != windows ]; then
  (cd .. && GOOS=darwin GOARCH=arm64 go build -tags tailnet -ldflags "-s -w $release" -o desktop/dist/deck-server ./cmd/deck)
  CGO_CFLAGS=-mmacosx-version-min=11.0 CGO_LDFLAGS=-mmacosx-version-min=11.0 go build -ldflags "-s -w" -o dist/deck-launcher .
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
  (cd dist && ditto -c -k --norsrc --noextattr --noqtn --keepParent deck.app deck-macos.zip)
fi

if [ "$what" != mac ]; then
  mkdir -p dist/windows
  (cd .. && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags tailnet -ldflags "-s -w $release" -o desktop/dist/windows/deck-linux ./cmd/deck)
  GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H windowsgui" -o dist/windows/deck.exe .
  cp build/deck.nsi build/deck.ico dist/windows/
  (cd dist/windows && makensis -V2 deck.nsi)
  mv dist/windows/deck-setup.exe dist/
  rm -rf dist/windows
fi
ls -la dist
