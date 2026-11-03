#!/bin/sh
set -eu

for argument in "$@"; do
  if [ "$argument" = "--info" ]; then
    printf '%s\n' \
      'Album Information:' \
      '  Title: Kind of Blue' \
      '  Artist: Miles Davis' \
      'Disc Information:' \
      'Area count: 2' \
      '  Area Information [0]:' \
      '  Track Count: 3' \
      '  Frame format encoding: [01] [DST]' \
      '  Speaker config: 2 Channel' \
      '  Track list of area[0]:' \
      '    Title[1]: So What' \
      '    Duration: 09:22:37 [mins:secs:frames]' \
      '    Title[2]: Freddie Freeloader' \
      '    Duration: 09:46:00 [mins:secs:frames]' \
      '    Title[3]: Blue in Green' \
      '    Duration: 05:37:00 [mins:secs:frames]' \
      '  Area Information [1]:' \
      '  Track Count: 3' \
      '  Frame format encoding: [01] [DSD]' \
      '  Speaker config: 6 Channel' \
      '  Track list of area[1]:' \
      '    Title[1]: So What' \
      '    Duration: 09:22:37 [mins:secs:frames]' \
      '    Title[2]: Freddie Freeloader' \
      '    Duration: 09:46:00 [mins:secs:frames]' \
      '    Title[3]: Blue in Green' \
      '    Duration: 05:37:00 [mins:secs:frames]'
    exit 0
  fi
done

output_dir=''
format='dsf'
pcm_rate='176400'
previous=''
for argument in "$@"; do
  if [ "$previous" = "--track-output-dir" ]; then output_dir=$argument; fi
  if [ "$previous" = "--flac-rate" ]; then pcm_rate=$argument; fi
  case "$argument" in
    --dsf) format='dsf' ;;
    --dsdiff) format='dff' ;;
    --flac) format='flac' ;;
  esac
  previous=$argument
done

if [ -z "$output_dir" ]; then
  printf 'mock: missing --track-output-dir\n' >&2
  exit 2
fi

album_dir="$output_dir/Kind of Blue"
mkdir -p "$album_dir"
track=1
for title in 'So What' 'Freddie Freeloader' 'Blue in Green'; do
  printf 'Processing [%s] (%d/3)..\n' "$title" "$track"
  printf 'Completed: 35%%\r'
  printf 'Completed: 100%%\n'
  number=$(printf '%02d' "$track")
  if [ "$format" = 'flac' ]; then
    ffmpeg -hide_banner -loglevel error -f lavfi -i 'anullsrc=r=44100:cl=stereo' -t 0.05 -ar "$pcm_rate" -c:a flac "$album_dir/$number - $title.flac"
  elif [ "$format" = 'dsf' ]; then
    # Minimal DSF header for metadata-only integration tests. The production
    # extractor writes the real DSD audio and format/data chunks.
    printf 'DSD \034\000\000\000\000\000\000\000\034\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000' > "$album_dir/$number - $title.dsf"
  else
    printf 'mock %s audio\n' "$format" > "$album_dir/$number - $title.$format"
  fi
  track=$((track + 1))
done

