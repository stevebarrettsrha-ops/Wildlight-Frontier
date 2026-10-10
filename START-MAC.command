#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
case "$(uname -m)" in arm64) launcher=runtime/wildlight-macos-arm64;; x86_64) launcher=runtime/wildlight-macos-amd64;; *) printf 'Unsupported processor. Use a 64-bit Intel or Apple silicon Mac.\n'; exit 1;; esac
chmod u+x "$launcher"
exec "./$launcher" "$@"
