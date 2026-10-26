#!/usr/bin/env bash
set -euo pipefail
exec "$(dirname "$0")/rehearse.sh" https_check.py
