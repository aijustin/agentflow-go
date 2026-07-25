#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
if [[ -z "${tag}" ]]; then
  echo "usage: $0 <vX.Y.Z>" >&2
  exit 2
fi

version="$(awk -F'"' '/^const Version = / { print $2; exit }' meta.go)"
if [[ -z "${version}" ]]; then
  echo "release version check: meta.go does not declare Version" >&2
  exit 1
fi

expected="v${version}"
if [[ "${tag}" != "${expected}" ]]; then
  echo "release version check: tag ${tag} does not match agentflow.Version ${expected}" >&2
  exit 1
fi

echo "release version check: ${tag}"
