#!/usr/bin/env bash
# Test and build ipod-bridge for the Pi 5 (Linux arm64) in Docker. Output: out/ipod-bridge
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
GO_IMAGE=golang:1.25
mkdir -p "$HERE/out"

docker run --rm -v "$HERE/bridge":/src -v iap-sink-gomod:/go/pkg/mod -v "$HERE/out":/out -w /src "$GO_IMAGE" sh -ec '
  [ -z "$(gofmt -l .)" ] || { gofmt -l .; echo "gofmt: format the files above"; exit 1; }
  go vet ./...
  go test -race -count=1 ./...
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ipod-bridge .
'
echo "Built $HERE/out/ipod-bridge"
