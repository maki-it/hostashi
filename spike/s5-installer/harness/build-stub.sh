#!/bin/bash
# Builds the stand-in hostashi binaries used by the tests (static, amd64 and arm64).
set -eu; cd "$(dirname "$0")/../stub"
for a in amd64 arm64; do CGO_ENABLED=0 GOARCH=$a go build -o ../harness/hostashi-stub-$a .; done
