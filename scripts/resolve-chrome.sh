#!/bin/sh
# Resolve the Chrome/Chromium binary for make check-layout.
#
# An explicit CHROME wins over every probe. Otherwise try the usual macOS app
# bundles and Linux package names so the layout gate is runnable without a
# macOS-only default in the Makefile.
set -eu

if [ -n "${CHROME:-}" ]; then
	printf '%s\n' "$CHROME"
	exit 0
fi

candidates='
/Applications/Google Chrome.app/Contents/MacOS/Google Chrome
/Applications/Chromium.app/Contents/MacOS/Chromium
google-chrome-stable
google-chrome
chromium
chromium-browser
'

while IFS= read -r candidate; do
	[ -n "$candidate" ] || continue
	if [ -x "$candidate" ] 2>/dev/null; then
		printf '%s\n' "$candidate"
		exit 0
	fi
	found=$(command -v "$candidate" 2>/dev/null || true)
	if [ -n "$found" ] && [ -x "$found" ]; then
		printf '%s\n' "$found"
		exit 0
	fi
done <<EOF
$candidates
EOF

exit 1
