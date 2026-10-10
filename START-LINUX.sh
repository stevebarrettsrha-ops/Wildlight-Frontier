#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
case "$(uname -m)" in aarch64|arm64) launcher=runtime/wildlight-linux-arm64;; x86_64|amd64) launcher=runtime/wildlight-linux-amd64;; *) printf 'Unsupported processor. Use 64-bit x86 or ARM Linux.\n'; exit 1;; esac
chmod u+x "$launcher"
exec "./$launcher" "$@"
