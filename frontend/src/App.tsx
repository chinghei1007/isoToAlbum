import { useEffect, useMemo, useRef, useState } from "react";
import type {
  ExportArea,
  ExportFormat,
  ExportSettings,
  Health,
  JobSnapshot,
  ScanSnapshot,
} from "./types";

type Phase = "ready" | "scanning" | "scanned" | "processing" | "complete" | "error";

const initialSettings: ExportSettings = {
  format: "dsf",
  area: "stereo",
  pcmRate: 176400,
  preserveDst: false,
  destination: "browser",
  destinationPath: "",
};

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1000)), units.length - 1);
  return `${(value / 1000 ** index).toFixed(index < 2 ? 0 : 2)} ${units[index]}`;
}

function percent(value: number) {
  return `${Math.round(Math.min(1, Math.max(0, value)) * 100)}%`;
}

function formatName(format: ExportFormat) {
  if (format === "dsf") return "Original · DSF";
  if (format === "dff") return "Original · DFF";
  return format.toUpperCase();
}

const imageExtensions = [".jpg", ".jpeg", ".png", ".webp"];
const preferredArtworkNames = ["cover", "folder", "front", "album"];
const directoryInputProps = { webkitdirectory: "", directory: "" };

function extension(filename: string) {
  const index = filename.lastIndexOf(".");
  return index >= 0 ? filename.slice(index).toLowerCase() : "";
}

function artworkRank(file: File) {
  const name = file.name.slice(0, Math.max(0, file.name.lastIndexOf("."))).toLowerCase();
  const preferred = preferredArtworkNames.indexOf(name);
  return preferred >= 0 ? preferred : preferredArtworkNames.length;
}

function chooseArtwork(files: File[]) {
  return files
    .filter((file) => imageExtensions.includes(extension(file.name)))
    .sort((left, right) => artworkRank(left) - artworkRank(right) || left.name.localeCompare(right.name))[0];
}

export default function App() {
  const [health, setHealth] = useState<Health>();
  const [phase, setPhase] = useState<Phase>("ready");
  const [scan, setScan] = useState<ScanSnapshot>();
  const [settings, setSettings] = useState(initialSettings);
  const [uploadProgress, setUploadProgress] = useState(0);
  const [job, setJob] = useState<JobSnapshot>();
  const [message, setMessage] = useState("");
  const [dragging, setDragging] = useState(false);
  const [artworkBusy, setArtworkBusy] = useState(false);
  const filesInputRef = useRef<HTMLInputElement>(null);
  const folderInputRef = useRef<HTMLInputElement>(null);
  const artworkInputRef = useRef<HTMLInputElement>(null);
  const eventsRef = useRef<EventSource | null>(null);
  const downloadStartedRef = useRef(false);

  useEffect(() => {
    fetch("/api/health")
      .then((response) => response.json())
      .then((value: Health) => setHealth(value))
      .catch(() => setHealth({ status: "error", sacdExtract: false, ffmpeg: false }));
    return () => eventsRef.current?.close();
  }, []);

  const busy = phase === "scanning" || phase === "processing" || artworkBusy;
  const hasStereo = scan?.areas.some((area) => area.id === "stereo") ?? false;
  const hasMultichannel = scan?.areas.some((area) => area.id === "multichannel") ?? false;
  const pcm = settings.format === "wav" || settings.format === "flac";
  const errors = useMemo(() => {
    const items: string[] = [];
    if (health && !health.sacdExtract) items.push("sacd_extract is required to read SACD ISO files.");
    if (health && !health.ffmpeg && (settings.format === "wav" || settings.format === "flac")) items.push("FFmpeg is required for WAV conversion and FLAC artwork embedding.");
    if (settings.destination === "custom" && !settings.destinationPath.trim()) items.push("Enter the other server output path.");
    return items;
  }, [health, settings.destination, settings.destinationPath, settings.format]);

  function reset() {
    eventsRef.current?.close();
    setPhase("ready");
    setScan(undefined);
    setJob(undefined);
    setMessage("");
    setUploadProgress(0);
    setArtworkBusy(false);
    setSettings(initialSettings);
    downloadStartedRef.current = false;
    if (filesInputRef.current) filesInputRef.current.value = "";
    if (folderInputRef.current) folderInputRef.current.value = "";
    if (artworkInputRef.current) artworkInputRef.current.value = "";
  }

  function acceptFiles(source?: FileList | File[]) {
    if (!source || busy) return;
    const files = Array.from(source);
    const isoFiles = files.filter((file) => extension(file.name) === ".iso");
    if (isoFiles.length !== 1) {
      setPhase("error");
      setMessage("Choose exactly one SACD image with the .iso extension.");
      return;
    }
    const file = isoFiles[0];
    const artwork = chooseArtwork(files);
    eventsRef.current?.close();
    setPhase("scanning");
    setMessage(artwork ? `Uploading the ISO and ${artwork.name} for a table-of-contents scan.` : "Uploading the ISO; a title cover will be generated after the scan.");
    setUploadProgress(0);
    setScan(undefined);
    setJob(undefined);
    downloadStartedRef.current = false;

    const body = new FormData();
    body.append("iso", file, file.name);
    if (artwork) body.append("artwork", artwork, artwork.name);
    const request = new XMLHttpRequest();
    request.open("POST", "/api/scans");
    request.upload.onprogress = (event) => {
      if (event.lengthComputable) setUploadProgress(event.loaded / event.total);
    };
    request.onerror = () => {
      setPhase("error");
      setMessage("The upload could not reach the local Go server.");
    };
    request.onload = () => {
      let value: ScanSnapshot | { error?: string };
      try {
        value = JSON.parse(request.responseText) as ScanSnapshot | { error?: string };
      } catch {
        value = { error: "The server returned an unreadable scan response." };
      }
      if (request.status < 200 || request.status >= 300 || !("id" in value)) {
        setPhase("error");
        setMessage(("error" in value ? value.error : undefined) || `The server rejected the ISO (${request.status}).`);
        return;
      }
      setScan(value);
      setSettings((current) => ({
        ...current,
        area: value.areas.some((area) => area.id === "stereo") ? "stereo" : "multichannel",
      }));
      setUploadProgress(1);
      setPhase("scanned");
      setMessage("");
    };
    request.send(body);
  }

  async function replaceArtwork(file?: File) {
    if (!file || !scan || busy) return;
    if (!imageExtensions.includes(extension(file.name))) {
      setPhase("error");
      setMessage("Choose a JPG, PNG, or WebP album image.");
      return;
    }
    setArtworkBusy(true);
    setMessage(`Normalizing ${file.name} as the canonical album cover.`);
    const body = new FormData();
    body.append("artwork", file, file.name);
    try {
      const response = await fetch(`/api/scans/${scan.id}/artwork`, { method: "POST", body });
      const value = await response.json() as ScanSnapshot | { error?: string };
      if (!response.ok || !("id" in value)) throw new Error(("error" in value ? value.error : undefined) || `The server rejected the image (${response.status}).`);
      setScan(value);
      setPhase("scanned");
      setMessage("");
    } catch (error) {
      setPhase("error");
      setMessage(error instanceof Error ? error.message : "Could not replace the album artwork.");
    } finally {
      setArtworkBusy(false);
      if (artworkInputRef.current) artworkInputRef.current.value = "";
    }
  }

  function applyJob(next: JobSnapshot) {
    setJob(next);
    setMessage(next.error || next.message || "");
    if (next.state === "complete") {
      setPhase("complete");
      eventsRef.current?.close();
      if (settings.destination === "browser" && next.downloadUrl && !downloadStartedRef.current) {
        downloadStartedRef.current = true;
        const link = document.createElement("a");
        link.href = next.downloadUrl;
        link.download = "";
        document.body.appendChild(link);
        link.click();
        link.remove();
      }
    } else if (next.state === "error") {
      setPhase("error");
      eventsRef.current?.close();
    } else {
      setPhase("processing");
    }
  }

  async function start() {
    if (!scan || busy || errors.length > 0) return;
    setPhase("processing");
    setMessage("Preparing the extraction job.");
    setJob(undefined);
    downloadStartedRef.current = false;
    try {
      const response = await fetch("/api/jobs", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ scanId: scan.id, ...settings }),
      });
      const value = await response.json() as JobSnapshot | { error?: string };
      if (!response.ok || !("id" in value)) throw new Error(("error" in value ? value.error : undefined) || `The server rejected the job (${response.status}).`);
      applyJob(value);
      const events = new EventSource(`/api/jobs/${value.id}/events`);
      eventsRef.current = events;
      events.onmessage = (event) => applyJob(JSON.parse(event.data) as JobSnapshot);
      events.onerror = () => {
        events.close();
        setMessage((current) => current || "The progress stream disconnected. The job may still be running.");
      };
    } catch (error) {
      setPhase("error");
      setMessage(error instanceof Error ? error.message : "Could not start the extraction.");
    }
  }

  function changeFormat(format: ExportFormat) {
    setSettings((current) => ({ ...current, format }));
  }

  function changeArea(area: ExportArea) {
    setSettings((current) => ({ ...current, area }));
  }

  const areaLabel = settings.area === "both" ? "stereo + multichannel" : settings.area;
  const summary = scan
    ? `Ready to export ${scan.tracks.length} ${areaLabel} tracks as ${formatName(settings.format)}${pcm ? ` · 24-bit / ${(settings.pcmRate / 1000).toFixed(1)} kHz` : " · native DSD"}.`
    : "Choose a SACD ISO to begin.";

  return (
    <main className="page">
      <header className="page-header">
        <h1>ISO to Album</h1>
        <p>Extract one SACD image into playable tracks with the local Go server.</p>
      </header>

      <div
        className={`drop-area${dragging ? " is-dragging" : ""}${busy ? " is-disabled" : ""}`}
        onDragEnter={(event) => { event.preventDefault(); if (!busy) setDragging(true); }}
        onDragOver={(event) => event.preventDefault()}
        onDragLeave={(event) => { if (event.currentTarget === event.target) setDragging(false); }}
        onDrop={(event) => {
          event.preventDefault();
          setDragging(false);
          acceptFiles(event.dataTransfer.files);
        }}
      >
        <p>Drop one SACD .iso with an optional album image</p>
        <div className="drop-actions">
          <button type="button" disabled={busy} onClick={() => folderInputRef.current?.click()}>Choose folder</button>
          <span>or</span>
          <button type="button" disabled={busy} onClick={() => filesInputRef.current?.click()}>Choose files</button>
        </div>
        <input
          ref={folderInputRef}
          className="visually-hidden"
          type="file"
          multiple
          {...directoryInputProps}
          disabled={busy}
          onChange={(event) => acceptFiles(event.currentTarget.files ?? undefined)}
        />
        <input
          ref={filesInputRef}
          className="visually-hidden"
          type="file"
          multiple
          accept=".iso,.jpg,.jpeg,.png,.webp,application/octet-stream,image/jpeg,image/png,image/webp"
          disabled={busy}
          onChange={(event) => acceptFiles(event.currentTarget.files ?? undefined)}
        />
      </div>

      {phase === "scanning" && (
        <section className="section-block" aria-live="polite">
          <div className="section-heading"><h2>Scanning</h2><span>{percent(uploadProgress)} uploaded</span></div>
          <div className="progress-track"><span style={{ width: percent(uploadProgress) }} /></div>
          <p className="quiet-note">{message}</p>
        </section>
      )}

      {scan && phase !== "scanning" && <>
        <section className="section-block" aria-labelledby="disc-heading">
          <div className="section-heading"><h2 id="disc-heading">Detected disc</h2><span>scanned without extracting audio</span></div>
          <div className="rows">
            <div className="input-row"><span>Source</span><span>{scan.filename}</span><span>{formatBytes(scan.size)}</span></div>
            <div className="input-row"><span>Album</span><span>{scan.album}{scan.artist ? ` · ${scan.artist}` : ""}</span><span>{scan.tracks.length} tracks</span></div>
            <div className="artwork-row">
              <span>Artwork</span>
              <img src={`${scan.artwork.url}?v=${scan.artwork.version}`} alt={`Album cover for ${scan.album}`} />
              <span className="artwork-details"><strong>{scan.artwork.filename}</strong><small>{scan.artwork.generated ? "Generated from album title and artist" : scan.artwork.selectedAutomatically ? `Selected automatically from ${scan.artwork.sourceName}` : `Replaced with ${scan.artwork.sourceName}`} · normalized RGB JPEG · {scan.artwork.width}×{scan.artwork.height}</small></span>
              <button type="button" className="quiet-button" disabled={busy} onClick={() => artworkInputRef.current?.click()}>{artworkBusy ? "Replacing" : "Replace"}</button>
              <input ref={artworkInputRef} className="visually-hidden" type="file" accept=".jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp" disabled={busy} onChange={(event) => replaceArtwork(event.currentTarget.files?.[0])} />
            </div>
            <div className="input-row"><span>Areas</span><span>{scan.areas.map((area) => `${area.label} · ${area.encoding}`).join(" · ")}</span><span>{scan.dst ? "DST present" : "Uncompressed DSD"}</span></div>
          </div>
        </section>

        <section className="section-block" aria-labelledby="tracks-heading">
          <div className="section-heading"><h2 id="tracks-heading">Tracks</h2><span>metadata from the SACD table of contents</span></div>
          <div className="rows">
            {scan.tracks.map((track) => {
              const state = job?.tracks.find((candidate) => candidate.index === track.index);
              return (
                <div className="track-row" key={track.index}>
                  <span>{String(track.index + 1).padStart(2, "0")}</span>
                  <span>{track.duration}</span>
                  <span className="track-title">{track.title}</span>
                  {state ? <div className="progress-cell"><div className="progress-track"><span style={{ width: percent(state.progress) }} /></div><span>{state.status}</span></div> : <span>{track.areas.map((area) => area === "stereo" ? "Stereo" : "MCH").join(" + ")}</span>}
                </div>
              );
            })}
          </div>
        </section>

        <section className="section-block" aria-labelledby="export-heading">
          <div className="section-heading"><h2 id="export-heading">Export</h2><span>settings stay with this disc</span></div>
          <div className="setting-grid">
            <label><span>Format</span><select aria-label="Format" disabled={busy} value={settings.format} onChange={(event) => changeFormat(event.target.value as ExportFormat)}>
              <option value="dsf">Original · DSF</option>
              <option value="dff">Original · DFF</option>
              <option value="wav">WAV · 24-bit PCM</option>
              <option value="flac">FLAC · 24-bit PCM</option>
            </select></label>
            <label><span>Area</span><select aria-label="Area" disabled={busy} value={settings.area} onChange={(event) => changeArea(event.target.value as ExportArea)}>
              {hasStereo && <option value="stereo">Stereo</option>}
              {hasMultichannel && <option value="multichannel">Multichannel</option>}
              {hasStereo && hasMultichannel && <option value="both">Stereo + multichannel</option>}
            </select></label>
            <label><span>Destination</span><select aria-label="Destination" disabled={busy} value={settings.destination} onChange={(event) => setSettings((current) => ({ ...current, destination: event.target.value as ExportSettings["destination"] }))}>
              <option value="browser">Browser Downloads · ZIP</option>
              <option value="project">Project root /output</option>
              <option value="custom">Other server path</option>
            </select></label>
          </div>

          {pcm && <div className="secondary-settings"><label><span>PCM sample rate</span><select aria-label="PCM sample rate" disabled={busy} value={settings.pcmRate} onChange={(event) => setSettings((current) => ({ ...current, pcmRate: Number(event.target.value) as ExportSettings["pcmRate"] }))}>
            <option value={88200}>88.2 kHz</option><option value={176400}>176.4 kHz</option><option value={352800}>352.8 kHz</option>
          </select></label></div>}

          {settings.format === "dff" && scan.dst && <label className="check-row"><input type="checkbox" disabled={busy} checked={settings.preserveDst} onChange={(event) => setSettings((current) => ({ ...current, preserveDst: event.target.checked }))} /><span><strong>Preserve DST compression</strong><small>Smaller and still lossless, but some Android players cannot decode it. Leave off for uncompressed DSD.</small></span></label>}

          {settings.destination === "custom" && <label className="path-field"><span>Server output path</span><input value={settings.destinationPath} disabled={busy} placeholder="/absolute/path or a path relative to the server root" onChange={(event) => setSettings((current) => ({ ...current, destinationPath: event.target.value }))} /></label>}
          {settings.destination === "project" && <p className="quiet-note">Tracks will be written under <code>output/&lt;album-name&gt;</code> in the server working directory.</p>}

          <div className="format-note">
            <p><strong>Artwork:</strong> <code>cover.jpg</code> is placed with the album. The same Front Cover is embedded into every DSF and FLAC track; DFF and WAV use the folder cover.</p>
            <p><strong>DSF:</strong> native DSD with lossless DST decompression when required; usually the safest portable-player choice.</p>
            <p><strong>DFF:</strong> native DSDIFF. Optional DST preservation saves space but needs player-side DST decoding.</p>
            <p><strong>WAV / FLAC:</strong> lossless 24-bit PCM files. DSD-to-PCM conversion is irreversible, so keep the ISO or DSF/DFF for DSD-level archiving.</p>
          </div>
        </section>

        {job && <section className="section-block" aria-live="polite"><div className="section-heading"><h2>Progress</h2><span>{percent(job.progress)}</span></div><div className="progress-track main-progress"><span style={{ width: percent(job.progress) }} /></div></section>}
      </>}

      {errors.length > 0 && <div className="message-list" role="alert">{errors.map((error) => <p key={error}>{error}</p>)}</div>}
      {message && phase !== "scanning" && <div className={`result-message${phase === "error" ? " is-error" : ""}`} role="status"><p>{message}</p>{job?.outputPath && <p>Output: <code>{job.outputPath}</code></p>}</div>}

      {scan && phase !== "scanning" && <div className="start-row"><p>{summary}</p><div className="start-actions">{(phase === "complete" || phase === "error") && <button type="button" className="quiet-button" onClick={reset}>Choose another ISO</button>}<button type="button" className="start-button" disabled={busy || errors.length > 0} onClick={start}>{phase === "processing" ? "Processing" : phase === "complete" ? "Export again" : "Start"}</button></div></div>}
    </main>
  );
}

