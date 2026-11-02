#!/usr/bin/env bash
set -euo pipefail
: "${APPLE_ID:?Missing Apple ID}"
: "${APPLE_TEAM_ID:?Missing Apple team ID}"
: "${APPLE_APP_PASSWORD:?Missing app-specific password}"
# Notarize each signed CLI before GoReleaser archives and publishes it.
# Bare Mach-O CLIs do not support stapling; Apple retains the ticket.
notary_dir="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/plib-notary.XXXXXX")"
trap 'rm -rf "$notary_dir"' EXIT
ditto -c -k --keepParent "$1" "$notary_dir/payload.zip"
xcrun notarytool submit "$notary_dir/payload.zip" --apple-id "$APPLE_ID" \
    --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" \
    --wait --timeout 30m --output-format json > "$notary_dir/result.json"
python3 - "$notary_dir/result.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    result = json.load(f)
if result.get('status') != 'Accepted':
    raise SystemExit('Apple notarization failed: ' + str(result))
print('Apple notarization accepted:', result['id'])
PY
