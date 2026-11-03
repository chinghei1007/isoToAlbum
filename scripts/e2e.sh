#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
port=18081
base_url="http://127.0.0.1:$port"
result_dir=$(mktemp -d /tmp/iso-to-album-e2e.XXXXXX)
server_pid=''

cleanup() {
  if [ -n "$server_pid" ]; then kill "$server_pid" >/dev/null 2>&1 || true; fi
  rm -rf "$result_dir"
}
trap cleanup EXIT INT TERM

SACD_EXTRACT_PATH="$repo_dir/testdata/mock_sacd_extract.sh" FFMPEG_PATH=${FFMPEG_PATH:-ffmpeg} "$repo_dir/bin/iso-to-album" -addr "127.0.0.1:$port" -root "$result_dir" >"$result_dir/server.log" 2>&1 &
server_pid=$!

attempt=0
until curl -fsS "$base_url/api/health" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 40 ]; then
    printf '%s\n' 'Server did not start.' >&2
    exit 1
  fi
  sleep 0.1
done

scan_json=$(curl -fsS -F "iso=@$repo_dir/testdata/demo.iso;filename=Kind of Blue.iso" "$base_url/api/scans")
scan_id=$(printf '%s' "$scan_json" | jq -r '.id')
test "$(printf '%s' "$scan_json" | jq -r '.album')" = 'Kind of Blue'
test "$(printf '%s' "$scan_json" | jq -r '.dst')" = 'true'

job_json=$(curl -fsS -H 'content-type: application/json' -d "{\"scanId\":\"$scan_id\",\"format\":\"dsf\",\"area\":\"stereo\",\"pcmRate\":176400,\"preserveDst\":false,\"destination\":\"browser\",\"destinationPath\":\"\"}" "$base_url/api/jobs")
job_id=$(printf '%s' "$job_json" | jq -r '.id')

attempt=0
while :; do
  status_json=$(curl -fsS "$base_url/api/jobs/$job_id")
  state=$(printf '%s' "$status_json" | jq -r '.state')
  if [ "$state" = 'complete' ]; then break; fi
  if [ "$state" = 'error' ]; then printf '%s\n' "$status_json" >&2; exit 1; fi
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 100 ]; then printf '%s\n' 'Job timed out.' >&2; exit 1; fi
  sleep 0.1
done

curl -fsS "$base_url/api/jobs/$job_id/download" -o "$result_dir/album.zip"
unzip -l "$result_dir/album.zip" | grep -q '01 - So What.dsf'
unzip -l "$result_dir/album.zip" | grep -q 'tracklist.txt'
printf '%s\n' 'E2E passed: scan, native DSF extraction, progress, and ZIP download.'
