package jobs

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"isotoalbum/internal/sacd"
)

type ScanSnapshot struct {
	ID       string       `json:"id"`
	Filename string       `json:"filename"`
	Size     int64        `json:"size"`
	Album    string       `json:"album"`
	Artist   string       `json:"artist"`
	Areas    []sacd.Area  `json:"areas"`
	Tracks   []sacd.Track `json:"tracks"`
	DST      bool         `json:"dst"`
}

type Manifest struct {
	ScanID          string `json:"scanId"`
	Format          string `json:"format"`
	Area            string `json:"area"`
	PCMRate         int    `json:"pcmRate"`
	PreserveDST     bool   `json:"preserveDst"`
	Destination     string `json:"destination"`
	DestinationPath string `json:"destinationPath"`
}

type TrackStatus struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Area     string  `json:"area"`
	Progress float64 `json:"progress"`
	Status   string  `json:"status"`
}

type Snapshot struct {
	ID          string        `json:"id"`
	State       string        `json:"state"`
	Stage       string        `json:"stage"`
	Message     string        `json:"message,omitempty"`
	Progress    float64       `json:"progress"`
	Tracks      []TrackStatus `json:"tracks"`
	DownloadURL string        `json:"downloadUrl,omitempty"`
	OutputPath  string        `json:"outputPath,omitempty"`
	Error       string        `json:"error,omitempty"`
}

type scan struct {
	id       string
	filename string
	size     int64
	tempDir  string
	input    string
	info     sacd.DiscInfo
}

type job struct {
	mu          sync.Mutex
	id          string
	state       string
	stage       string
	message     string
	progress    float64
	tracks      []TrackStatus
	downloadURL string
	outputPath  string
	err         string
	zipPath     string
	workDir     string
	scan        *scan
	manifest    Manifest
	subscribers map[chan Snapshot]struct{}
}

type Manager struct {
	mu          sync.RWMutex
	scans       map[string]*scan
	jobs        map[string]*job
	tool        *sacd.Tool
	projectRoot string
}

func NewManager(tool *sacd.Tool, projectRoot string) *Manager {
	return &Manager{scans: map[string]*scan{}, jobs: map[string]*job{}, tool: tool, projectRoot: projectRoot}
}

func (m *Manager) Inspect(ctx context.Context, tempDir, input, filename string, size int64) (ScanSnapshot, error) {
	info, err := m.tool.Inspect(ctx, input)
	if err != nil {
		return ScanSnapshot{}, err
	}
	id, err := randomID()
	if err != nil {
		return ScanSnapshot{}, err
	}
	item := &scan{id: id, filename: filename, size: size, tempDir: tempDir, input: input, info: info}
	m.mu.Lock()
	m.scans[id] = item
	m.mu.Unlock()
	return item.snapshot(), nil
}

func (s *scan) snapshot() ScanSnapshot {
	return ScanSnapshot{ID: s.id, Filename: s.filename, Size: s.size, Album: s.info.Album, Artist: s.info.Artist, Areas: append([]sacd.Area(nil), s.info.Areas...), Tracks: append([]sacd.Track(nil), s.info.Tracks...), DST: s.info.DST}
}

func (m *Manager) Create(manifest Manifest) (Snapshot, error) {
	m.mu.RLock()
	source, ok := m.scans[manifest.ScanID]
	m.mu.RUnlock()
	if !ok {
		return Snapshot{}, fmt.Errorf("the scanned ISO is no longer available; choose it again")
	}
	if err := validateManifest(source, manifest); err != nil {
		return Snapshot{}, err
	}
	id, err := randomID()
	if err != nil {
		return Snapshot{}, err
	}
	workDir, err := os.MkdirTemp("", "iso-to-album-job-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("create job directory: %w", err)
	}
	tracks := make([]TrackStatus, 0, len(source.info.Tracks))
	for _, track := range source.info.Tracks {
		if trackMatchesArea(track, manifest.Area) {
			tracks = append(tracks, TrackStatus{Index: track.Index, Name: track.Title, Area: manifest.Area, Status: "waiting"})
		}
	}
	item := &job{id: id, state: "queued", stage: "queued", message: "Waiting to extract the SACD tracks.", tracks: tracks, workDir: workDir, scan: source, manifest: manifest, subscribers: map[chan Snapshot]struct{}{}}
	if manifest.Destination == "browser" {
		item.downloadURL = "/api/jobs/" + id + "/download"
	}
	m.mu.Lock()
	m.jobs[id] = item
	m.mu.Unlock()
	go m.process(item)
	return item.snapshot(), nil
}

func validateManifest(source *scan, manifest Manifest) error {
	switch manifest.Format {
	case "dsf", "dff":
	case "wav", "flac":
		if manifest.PCMRate != 88200 && manifest.PCMRate != 176400 && manifest.PCMRate != 352800 {
			return fmt.Errorf("choose a supported PCM sample rate")
		}
	default:
		return fmt.Errorf("unsupported output format %q", manifest.Format)
	}
	available := map[string]bool{}
	for _, area := range source.info.Areas {
		available[area.ID] = true
	}
	switch manifest.Area {
	case "stereo", "multichannel":
		if !available[manifest.Area] {
			return fmt.Errorf("the ISO does not contain a %s area", manifest.Area)
		}
	case "both":
		if !available["stereo"] || !available["multichannel"] {
			return fmt.Errorf("both stereo and multichannel areas are required for this choice")
		}
	default:
		return fmt.Errorf("unsupported SACD area %q", manifest.Area)
	}
	if manifest.Destination != "browser" && manifest.Destination != "project" && manifest.Destination != "custom" {
		return fmt.Errorf("unsupported destination %q", manifest.Destination)
	}
	if manifest.Destination == "custom" && strings.TrimSpace(manifest.DestinationPath) == "" {
		return fmt.Errorf("custom output path is required")
	}
	return nil
}

func trackMatchesArea(track sacd.Track, selected string) bool {
	if selected == "both" {
		return true
	}
	for _, area := range track.Areas {
		if area == selected {
			return true
		}
	}
	return false
}

func (m *Manager) process(item *job) {
	filesDir := filepath.Join(item.workDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		item.fail(err)
		return
	}
	item.update(func() {
		item.state = "processing"
		item.stage = "extracting"
		item.message = "Extracting native SACD tracks."
	})

	options := sacd.ExtractOptions{Format: item.manifest.Format, Area: item.manifest.Area, PCMRate: item.manifest.PCMRate, PreserveDST: item.manifest.PreserveDST}
	err := m.tool.Extract(context.Background(), item.scan.input, filesDir, options, func(progress sacd.Progress) {
		item.applyProgress(progress)
	})
	if err != nil {
		item.fail(err)
		return
	}

	if item.manifest.Format == "wav" {
		item.update(func() {
			item.stage = "converting"
			item.progress = 0.9
			item.message = "Converting the lossless PCM intermediates to WAV."
		})
		if err := m.tool.ConvertFLACToWAV(context.Background(), filesDir, func(current, total int, name string) {
			item.update(func() {
				item.progress = 0.9 + 0.08*float64(current)/float64(total)
				item.message = fmt.Sprintf("Writing WAV %d of %d: %s", current, total, name)
			})
		}); err != nil {
			item.fail(err)
			return
		}
	}

	if err := writeTrackList(item.scan.info.Tracks, filesDir); err != nil {
		item.fail(err)
		return
	}

	item.update(func() {
		item.stage = "packaging"
		item.progress = 0.98
		item.message = "Finalizing the album output."
	})
	if item.manifest.Destination == "browser" {
		zipPath := filepath.Join(item.workDir, cleanName(item.scan.info.Album, "sacd-album")+".zip")
		if err := zipDirectory(filesDir, zipPath); err != nil {
			item.fail(err)
			return
		}
		item.mu.Lock()
		item.zipPath = zipPath
		item.mu.Unlock()
	} else {
		outputPath, err := m.outputPath(item)
		if err != nil {
			item.fail(err)
			return
		}
		if err := sacd.CopyTree(filesDir, outputPath); err != nil {
			item.fail(fmt.Errorf("write album output: %w", err))
			return
		}
		item.mu.Lock()
		item.outputPath = outputPath
		item.mu.Unlock()
	}

	item.update(func() {
		item.state = "complete"
		item.stage = "complete"
		item.progress = 1
		item.message = "All tracks are ready."
		for index := range item.tracks {
			item.tracks[index].Status = "complete"
			item.tracks[index].Progress = 1
		}
	})
}

func (item *job) applyProgress(progress sacd.Progress) {
	item.update(func() {
		item.stage = "extracting"
		item.message = progress.Message
		if progress.Total > 0 {
			completed := float64(max(progress.Current-1, 0))
			item.progress = 0.9 * (completed + progress.Value) / float64(progress.Total)
		}
		if len(item.tracks) == 0 || progress.Current <= 0 {
			return
		}
		index := (progress.Current - 1) % len(item.tracks)
		for trackIndex := range item.tracks {
			if trackIndex < index {
				item.tracks[trackIndex].Status = "complete"
				item.tracks[trackIndex].Progress = 1
			} else if trackIndex == index {
				item.tracks[trackIndex].Status = "processing"
				item.tracks[trackIndex].Progress = progress.Value
			}
		}
	})
}

func (m *Manager) outputPath(item *job) (string, error) {
	base := filepath.Join(m.projectRoot, "output")
	if item.manifest.Destination == "custom" {
		base = strings.TrimSpace(item.manifest.DestinationPath)
		if strings.HasPrefix(base, "~"+string(os.PathSeparator)) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, strings.TrimPrefix(base, "~"+string(os.PathSeparator)))
		}
		if !filepath.IsAbs(base) {
			base = filepath.Join(m.projectRoot, base)
		}
	}
	base = filepath.Clean(base)
	name := cleanName(item.scan.info.Album, "sacd-album")
	candidate := filepath.Join(base, name)
	if _, err := os.Stat(candidate); err == nil {
		candidate += "-" + item.id[:6]
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return candidate, nil
}

func (m *Manager) Snapshot(id string) (Snapshot, bool) {
	item, ok := m.getJob(id)
	if !ok {
		return Snapshot{}, false
	}
	return item.snapshot(), true
}

func (m *Manager) Subscribe(id string) (<-chan Snapshot, func(), bool) {
	item, ok := m.getJob(id)
	if !ok {
		return nil, nil, false
	}
	updates := make(chan Snapshot, 1)
	item.mu.Lock()
	item.subscribers[updates] = struct{}{}
	updates <- item.snapshotLocked()
	item.mu.Unlock()
	cancel := func() {
		item.mu.Lock()
		if _, exists := item.subscribers[updates]; exists {
			delete(item.subscribers, updates)
			close(updates)
		}
		item.mu.Unlock()
	}
	return updates, cancel, true
}

func (m *Manager) Download(id string) (string, string, bool) {
	item, ok := m.getJob(id)
	if !ok {
		return "", "", false
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.state != "complete" || item.zipPath == "" {
		return "", "", false
	}
	return item.zipPath, filepath.Base(item.zipPath), true
}

func (m *Manager) getJob(id string) (*job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.jobs[id]
	return item, ok
}

func (item *job) snapshot() Snapshot {
	item.mu.Lock()
	defer item.mu.Unlock()
	return item.snapshotLocked()
}

func (item *job) snapshotLocked() Snapshot {
	return Snapshot{ID: item.id, State: item.state, Stage: item.stage, Message: item.message, Progress: item.progress, Tracks: append([]TrackStatus(nil), item.tracks...), DownloadURL: item.downloadURL, OutputPath: item.outputPath, Error: item.err}
}

func (item *job) update(change func()) {
	item.mu.Lock()
	change()
	item.publishLocked()
	item.mu.Unlock()
}

func (item *job) fail(err error) {
	item.update(func() {
		item.state = "error"
		item.stage = "error"
		item.err = err.Error()
		item.message = "Extraction stopped."
	})
}

func (item *job) publishLocked() {
	snapshot := item.snapshotLocked()
	for subscriber := range item.subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			subscriber <- snapshot
		}
	}
}

func writeTrackList(tracks []sacd.Track, root string) error {
	var builder strings.Builder
	builder.WriteString("\ufeff")
	for _, track := range tracks {
		fmt.Fprintf(&builder, "%02d. %s\n", track.Index+1, track.Title)
	}
	return os.WriteFile(filepath.Join(root, "tracklist.txt"), []byte(builder.String()), 0o644)
}

func zipDirectory(source, target string) error {
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	archiveErr := archive.Close()
	closeErr := output.Close()
	if walkErr != nil {
		return walkErr
	}
	if archiveErr != nil {
		return archiveErr
	}
	return closeErr
}

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func cleanName(value, fallback string) string {
	value = strings.TrimSpace(unsafeName.ReplaceAllString(filepath.Base(value), "-"))
	value = strings.Trim(value, ". ")
	if value == "" {
		return fallback
	}
	return value
}

func randomID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
