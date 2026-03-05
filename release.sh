#!/bin/bash
set -e

VERSION="${1:?Usage: ./release.sh v0.1.0}"
BINARY="iatan"
EXTRAS="config.json firstrun.json"
OUTDIR="release"

rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"

LDFLAGS="-s -w -X main.Version=$VERSION"

echo "Building $VERSION..."

# Windows AMD64
echo "  windows/amd64"
GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$OUTDIR/$BINARY.exe" .
cd "$OUTDIR" && zip -q "IATAN_${VERSION}_windows_amd64.zip" "$BINARY.exe" && cd ..
cp $EXTRAS "$OUTDIR/"
cd "$OUTDIR" && zip -q "IATAN_${VERSION}_windows_amd64.zip" $EXTRAS && cd ..
rm "$OUTDIR/$BINARY.exe"

# Linux AMD64
echo "  linux/amd64"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$OUTDIR/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_amd64.tar.gz" -C "$OUTDIR" "$BINARY" $EXTRAS
rm "$OUTDIR/$BINARY"

# Linux ARM64
echo "  linux/arm64"
GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$OUTDIR/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_linux_arm64.tar.gz" -C "$OUTDIR" "$BINARY" $EXTRAS
rm "$OUTDIR/$BINARY"

# macOS AMD64
echo "  darwin/amd64"
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$OUTDIR/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_amd64.tar.gz" -C "$OUTDIR" "$BINARY" $EXTRAS
rm "$OUTDIR/$BINARY"

# macOS ARM64
echo "  darwin/arm64"
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$OUTDIR/$BINARY" .
tar -czf "$OUTDIR/IATAN_${VERSION}_darwin_arm64.tar.gz" -C "$OUTDIR" "$BINARY" $EXTRAS
rm "$OUTDIR/$BINARY"

# Clean up copied extras
rm -f "$OUTDIR/config.json" "$OUTDIR/firstrun.json"

echo ""
echo "Done! Release files in $OUTDIR/:"
ls -lh "$OUTDIR/"
