#!/bin/bash
set -e

VERSION="${1:?Usage: ./release.sh v0.1.0}"
BINARY="iatan"
EXTRAS="config.json firstrun.json"
WIN_EXTRAS="start.bat"
UNIX_EXTRAS="start.sh"
OUTDIR="release"
STAGING="release/staging/IATAN"

rm -rf "$OUTDIR"
mkdir -p "$STAGING"

LDFLAGS="-s -w -X main.Version=$VERSION"

echo "Building $VERSION..."

# Copy extras into the IATAN directory inside staging.
for f in $EXTRAS; do
  [ -f "$f" ] && cp "$f" "$STAGING/"
done

# Windows AMD64 (.zip — native for Windows users)
echo "  windows/amd64"
GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY.exe" .
for f in $WIN_EXTRAS; do
  [ -f "$f" ] && cp "$f" "$STAGING/"
done
powershell -NoProfile -Command "Compress-Archive -Path 'release/staging/IATAN' -DestinationPath 'release/IATAN_${VERSION}_windows_amd64.zip'"
rm "$STAGING/$BINARY.exe"
for f in $WIN_EXTRAS; do rm -f "$STAGING/$f"; done

# Add unix extras for Linux/macOS builds.
for f in $UNIX_EXTRAS; do
  [ -f "$f" ] && cp "$f" "$STAGING/"
done

# Linux AMD64
echo "  linux/amd64"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_amd64.tar.gz" -C release/staging IATAN/
rm "$STAGING/$BINARY"

# Linux ARM64
echo "  linux/arm64"
GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_arm64.tar.gz" -C release/staging IATAN/
rm "$STAGING/$BINARY"

# macOS AMD64
echo "  darwin/amd64"
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_amd64.tar.gz" -C release/staging IATAN/
rm "$STAGING/$BINARY"

# macOS ARM64
echo "  darwin/arm64"
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_arm64.tar.gz" -C release/staging IATAN/
rm "$STAGING/$BINARY"

# Clean up staging
rm -rf release/staging

echo ""
echo "Done! Release files in $OUTDIR/:"
ls -lh "$OUTDIR/"
