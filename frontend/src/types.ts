export type DiscArea = {
  id: "stereo" | "multichannel";
  label: string;
  channels: number;
  encoding: string;
  trackCount: number;
};

export type DiscTrack = {
  index: number;
  title: string;
  performer?: string;
  duration: string;
  durationSeconds: number;
  areas: string[];
};

export type ScanSnapshot = {
  id: string;
  filename: string;
  size: number;
  album: string;
  artist: string;
  areas: DiscArea[];
  tracks: DiscTrack[];
  dst: boolean;
  artwork: {
    filename: string;
    sourceName: string;
    generated: boolean;
    selectedAutomatically: boolean;
    width: number;
    height: number;
    url: string;
    version: number;
  };
};

export type Health = {
  status: string;
  sacdExtract: boolean;
  ffmpeg: boolean;
};

export type ExportFormat = "dsf" | "dff" | "wav" | "flac";
export type ExportArea = "stereo" | "multichannel" | "both";
export type Destination = "browser" | "project" | "custom";

export type ExportSettings = {
  format: ExportFormat;
  area: ExportArea;
  pcmRate: 88200 | 176400 | 352800;
  preserveDst: boolean;
  destination: Destination;
  destinationPath: string;
};

export type JobTrack = {
  index: number;
  name: string;
  area: string;
  progress: number;
  status: string;
};

export type JobSnapshot = {
  id: string;
  state: "queued" | "processing" | "complete" | "error";
  stage: string;
  message?: string;
  progress: number;
  tracks: JobTrack[];
  downloadUrl?: string;
  outputPath?: string;
  error?: string;
};

