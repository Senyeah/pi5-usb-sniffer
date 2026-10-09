#!/usr/bin/env bash
# Test and build iap-sink for the Pi 3 (Linux arm64) in Docker. Output: tools/test-stereo/out/iap-sink
# Optional: IAP_CAPTURE_TIMELINE and IAP_REAL_CERT (paths on the Mac) switch on the tests against a private capture.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
GO_IMAGE=golang:1.25
mkdir -p "$HERE/out"

mounts=() envs=()
if [ -n "${IAP_CAPTURE_TIMELINE:-}" ] && [ -n "${IAP_REAL_CERT:-}" ]; then
  mounts+=(-v "$IAP_CAPTURE_TIMELINE:/cap/timeline.jsonl:ro" -v "$IAP_REAL_CERT:/cap/cert.p7b:ro")
  envs+=(-e IAP_CAPTURE_TIMELINE=/cap/timeline.jsonl -e IAP_REAL_CERT=/cap/cert.p7b)
fi

docker run --rm -v "$HERE/iap-sink":/src -v iap-sink-gomod:/go/pkg/mod -v "$HERE/out":/out \
  ${mounts[@]+"${mounts[@]}"} ${envs[@]+"${envs[@]}"} -w /src "$GO_IMAGE" sh -ec '
  [ -z "$(gofmt -l .)" ] || { gofmt -l .; echo "gofmt: format the files above"; exit 1; }
  go vet ./...
  go test -race -count=1 ./...
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/iap-sink .
'
echo "Built $HERE/out/iap-sink"
