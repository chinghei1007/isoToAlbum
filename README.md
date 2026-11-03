# ISO to Album

ISO to Album is a local, single-port web application for turning a readable SACD ISO into per-track audio files. Its Go server scans and extracts the disc with `sacd_extract`, serves an embedded Vite/React interface, and uses FFmpeg for WAV conversion and lossless FLAC artwork attachment.

The interface follows the same quiet, powder-blue design language as the sibling `cueSplitter` project.

## Output choices

- **Original · DSF** extracts native DSD tracks. DST-compressed material is decompressed losslessly because DSF stores DSD rather than DST. This is normally the most compatible DSD choice for portable players.
- **Original · DFF** extracts DSDIFF tracks. By default DST is decompressed losslessly for compatibility; enable **Preserve DST compression** to keep the smaller DST payload when the player can decode it.
- **FLAC · 24-bit PCM** uses `sacd_extract`'s DSD-to-PCM path at 88.2, 176.4, or 352.8 kHz. The approved default is 176.4 kHz.
- **WAV · 24-bit PCM** starts from the same lossless FLAC PCM intermediate and uses FFmpeg to write 24-bit WAV, so WAV and FLAC contain the same PCM program material at a chosen rate.

DSF, DFF, and DST are not inherently lossy. DST is lossless compression for DSD. DSD-to-PCM conversion is irreversible even when the resulting WAV or FLAC is losslessly stored, so keep the ISO or DSF/DFF files when DSD-level archival preservation matters.

## Dependencies

Build dependencies:

- Go 1.24 or newer
- Node.js and npm

Runtime dependencies:

- A current `sacd_extract` build with `--dsf`, `--dsdiff`, `--flac`, and `--flac-rate` support. This project targets [dev-zetta/sacd_extract2](https://github.com/dev-zetta/sacd_extract2).
- FFmpeg for WAV output and FLAC artwork embedding. DSF and DFF do not require FFmpeg.

Check the environment:

```sh
./scripts/setup.sh --check
```

If `sacd_extract` is not on `PATH`, set an explicit location:

```sh
export SACD_EXTRACT_PATH=/absolute/path/to/sacd_extract
```

`sacd_extract` is GPL-2.0 software and is not vendored by this repository. Review its license before redistributing a combined application bundle.

## Build and run

```sh
./scripts/build.sh
./bin/iso-to-album
```

Open <http://127.0.0.1:8081>. The server binds to loopback by default.

During frontend development:

```sh
go run ./cmd/isotoalbum
npm --prefix frontend run dev
```

Vite proxies `/api` to the Go server on port 8080 only when configured there; for the default Go port, run `go run ./cmd/isotoalbum -addr 127.0.0.1:8080` alongside Vite.

## Workflow

1. Choose a folder or files containing exactly one readable SACD `.iso` and, optionally, JPG, PNG, or WebP artwork. When several images are present, common names such as `cover`, `folder`, `front`, and `album` are preferred.
2. The server runs `sacd_extract --info`, displays the disc metadata, and normalizes the selected image to an RGB `cover.jpg` no larger than 1000 px. If no image is supplied, it generates a title cover from the album and artist metadata.
3. Review the canonical cover or replace it before export.
4. Choose DSF, DFF, WAV, or FLAC; stereo is selected when present. Multichannel and combined exports appear only when the ISO contains those areas.
5. Choose Browser Downloads, the project `output/` directory, or another server path.
6. Start the job and follow per-track progress. Browser output is delivered as a ZIP containing the extracted album, `cover.jpg`, and a UTF-8 `tracklist.txt`. The same Front Cover is embedded in DSF and FLAC tracks without re-encoding their audio; DFF and WAV use the folder cover.

The upload is local but still copies the ISO into a temporary server directory. Temporary scan and job directories last for the lifetime of the server process and the operating system's normal temporary-file cleanup.

## Checks

```sh
npm --prefix frontend run typecheck
npm --prefix frontend run build
go test ./...
go vet ./...
./scripts/e2e.sh
```

The E2E script uses `testdata/mock_sacd_extract.sh` and a tiny mock `.iso`; it does not contain copyrighted SACD data.
