#!/bin/sh
# Runs before 20-envsubst-on-templates.sh: without the policies the templates would render
# empty security headers, so the container stops instead.
set -eu
for name in UI__CONTENT_SECURITY_POLICY UI__CONNECTION_ALLOWLIST; do
    if [ -z "$(printenv "$name" || true)" ]; then
        echo "$name is required" >&2
        exit 1
    fi
done
