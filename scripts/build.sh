#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

cd "$repo_dir/frontend"
npm ci
npm run build

cd "$repo_dir"
mkdir -p bin
go build -o bin/iso-to-album ./cmd/isotoalbum

printf 'Built %s/bin/iso-to-album\n' "$repo_dir"

