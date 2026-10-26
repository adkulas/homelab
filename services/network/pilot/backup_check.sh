#!/usr/bin/env bash
set -euo pipefail
exec "$(dirname "$0")/rehearse.sh" backup_check.py
