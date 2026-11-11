#!/usr/bin/env bash
set -euo pipefail
repo=$(git rev-parse --show-toplevel)
context="$repo/sites/home/hosts/homelab/ansible"
if [[ -n $(docker ps -aq --filter label=com.docker.compose.project=home-traefik) ]]; then
    echo 'Refusing rehearsal: home-traefik already exists on this Docker daemon.' >&2
    exit 1
fi
work=$(mktemp -d /tmp/homelab-traefik-test.XXXXXX)
trap 'rm -rf "$work"' EXIT
docker build -q -t homelab-traefik-ansible-test -f "$context/tests/Containerfile" "$context" >/dev/null
docker run --rm --network host \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$repo:/repository:ro" -v "$work:$work" \
    -e TRAEFIK_TEST_WORK="$work" \
    homelab-traefik-ansible-test python /repository/sites/home/hosts/homelab/ansible/tests/test_traefik.py "$@"
