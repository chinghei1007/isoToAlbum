#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
missing=''

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    missing="$missing $1"
  fi
}

require_command go
require_command node
require_command npm

ffmpeg_path=${FFMPEG_PATH:-ffmpeg}
if [ -n "${SACD_EXTRACT_PATH:-}" ]; then
  extract_path=$SACD_EXTRACT_PATH
elif [ -x "$repo_dir/bin/sacd_extract" ]; then
  extract_path="$repo_dir/bin/sacd_extract"
else
  extract_path=sacd_extract
fi

if ! command -v "$ffmpeg_path" >/dev/null 2>&1 && [ ! -x "$ffmpeg_path" ]; then
  missing="$missing ffmpeg"
fi
if ! command -v "$extract_path" >/dev/null 2>&1 && [ ! -x "$extract_path" ]; then
  missing="$missing sacd_extract"
fi

if [ -n "$missing" ]; then
  printf 'Missing dependencies:%s\n' "$missing" >&2
  printf '%s\n' 'Install Go, Node.js/npm, and FFmpeg with your platform package manager.' >&2
  printf '%s\n' 'Build a current sacd_extract release from https://github.com/dev-zetta/sacd_extract2 and place it on PATH,' >&2
  printf '%s\n' 'or set SACD_EXTRACT_PATH=/absolute/path/to/sacd_extract.' >&2
  exit 1
fi

printf '%s\n' 'All build and runtime dependencies are available.'

