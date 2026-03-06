#!/bin/bash
set -e

VERSION="${1:?Usage: ./release.sh v0.1.0}"
BINARY="iatan"
EXTRAS="config.json firstrun.json"
OUTDIR="release"
STAGING="release/staging"

rm -rf "$OUTDIR"
mkdir -p "$STAGING"

LDFLAGS="-s -w -X main.Version=$VERSION"

echo "Building $VERSION..."

# Copy extras into staging so tar/zip can bundle them alongside the binary.
cp $EXTRAS "$STAGING/"

# Windows AMD64
echo "  windows/amd64"
GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY.exe" .
tar -czf "$OUTDIR/IATAN_${VERSION}_windows_amd64.tar.gz" -C "$STAGING" "$BINARY.exe" $EXTRAS
rm "$STAGING/$BINARY.exe"

# Linux AMD64
echo "  linux/amd64"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_amd64.tar.gz" -C "$STAGING" "$BINARY" $EXTRAS
rm "$STAGING/$BINARY"

# Linux ARM64
echo "  linux/arm64"
GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_arm64.tar.gz" -C "$STAGING" "$BINARY" $EXTRAS
rm "$STAGING/$BINARY"

# macOS AMD64
echo "  darwin/amd64"
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_amd64.tar.gz" -C "$STAGING" "$BINARY" $EXTRAS
rm "$STAGING/$BINARY"

# macOS ARM64
echo "  darwin/arm64"
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$STAGING/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_arm64.tar.gz" -C "$STAGING" "$BINARY" $EXTRAS
rm "$STAGING/$BINARY"

# Clean up staging
rm -rf "$STAGING"

echo ""
echo "Done! Release files in $OUTDIR/:"
ls -lh "$OUTDIR/"
