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

generated_scan=$(curl -fsS -F "iso=@$repo_dir/testdata/demo.iso;filename=Kind of Blue.iso" "$base_url/api/scans")
generated_scan_id=$(printf '%s' "$generated_scan" | jq -r '.id')
test "$(printf '%s' "$generated_scan" | jq -r '.album')" = 'Kind of Blue'
test "$(printf '%s' "$generated_scan" | jq -r '.artwork.generated')" = 'true'
curl -fsS "$base_url/api/scans/$generated_scan_id/artwork" -o "$result_dir/generated-cover.jpg"
test -s "$result_dir/generated-cover.jpg"

scan_json=$(curl -fsS -F "iso=@$repo_dir/testdata/demo.iso;filename=Kind of Blue.iso" -F "artwork=@$result_dir/generated-cover.jpg;filename=front.jpg" "$base_url/api/scans")
scan_id=$(printf '%s' "$scan_json" | jq -r '.id')
test "$(printf '%s' "$scan_json" | jq -r '.artwork.generated')" = 'false'
test "$(printf '%s' "$scan_json" | jq -r '.artwork.sourceName')" = 'front.jpg'
test "$(printf '%s' "$scan_json" | jq -r '.dst')" = 'true'

wait_for_job() {
  waiting_id=$1
  attempt=0
  while :; do
    status_json=$(curl -fsS "$base_url/api/jobs/$waiting_id")
    state=$(printf '%s' "$status_json" | jq -r '.state')
    if [ "$state" = 'complete' ]; then return; fi
    if [ "$state" = 'error' ]; then printf '%s\n' "$status_json" >&2; exit 1; fi
    attempt=$((attempt + 1))
    if [ "$attempt" -gt 100 ]; then printf '%s\n' 'Job timed out.' >&2; exit 1; fi
    sleep 0.1
  done
}

job_json=$(curl -fsS -H 'content-type: application/json' -d "{\"scanId\":\"$scan_id\",\"format\":\"dsf\",\"area\":\"stereo\",\"pcmRate\":176400,\"preserveDst\":false,\"destination\":\"browser\",\"destinationPath\":\"\"}" "$base_url/api/jobs")
job_id=$(printf '%s' "$job_json" | jq -r '.id')
wait_for_job "$job_id"

curl -fsS "$base_url/api/jobs/$job_id/download" -o "$result_dir/album.zip"
unzip -l "$result_dir/album.zip" | grep -q '01 - So What.dsf'
unzip -l "$result_dir/album.zip" | grep -q 'Kind of Blue/cover.jpg'
unzip -l "$result_dir/album.zip" | grep -q 'tracklist.txt'
unzip -p "$result_dir/album.zip" 'Kind of Blue/01 - So What.dsf' | LC_ALL=C grep -a -q 'APIC'

flac_job_json=$(curl -fsS -H 'content-type: application/json' -d "{\"scanId\":\"$scan_id\",\"format\":\"flac\",\"area\":\"stereo\",\"pcmRate\":176400,\"preserveDst\":false,\"destination\":\"browser\",\"destinationPath\":\"\"}" "$base_url/api/jobs")
flac_job_id=$(printf '%s' "$flac_job_json" | jq -r '.id')
wait_for_job "$flac_job_id"
curl -fsS "$base_url/api/jobs/$flac_job_id/download" -o "$result_dir/flac.zip"
mkdir -p "$result_dir/flac"
unzip -oq "$result_dir/flac.zip" -d "$result_dir/flac"
test "$(ffprobe -v error -select_streams a:0 -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$result_dir/flac/Kind of Blue/01 - So What.flac")" = 'flac'
test "$(ffprobe -v error -select_streams v:0 -show_entries stream_disposition=attached_pic -of default=noprint_wrappers=1:nokey=1 "$result_dir/flac/Kind of Blue/01 - So What.flac")" = '1'

printf '%s\n' 'E2E passed: generated/uploaded artwork, native DSF APIC, FLAC attached cover, progress, and ZIP downloads.'
