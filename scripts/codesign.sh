#!/usr/bin/env bash
set -euo pipefail
: "${APPLE_SIGNING_IDENTITY:?Missing Developer ID Application signing identity}"
codesign --force --options runtime --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$1"
codesign --verify --strict --verbose=2 "$1"
scripts/notarize.sh "$1"
