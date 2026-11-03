package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"isotoalbum/internal/jobs"
	"isotoalbum/internal/sacd"
	"isotoalbum/web"
)

const maxUploadSize int64 = 20 << 30

type api struct {
	manager *jobs.Manager
	tool    *sacd.Tool
}

func New(manager *jobs.Manager, tool *sacd.Tool) (http.Handler, error) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}
	service := &api{manager: manager, tool: tool}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", service.health)
	mux.HandleFunc("POST /api/scans", service.createScan)
	mux.HandleFunc("GET /api/scans/{id}/artwork", service.scanArtwork)
	mux.HandleFunc("POST /api/scans/{id}/artwork", service.replaceArtwork)
	mux.HandleFunc("POST /api/jobs", service.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", service.jobStatus)
	mux.HandleFunc("GET /api/jobs/{id}/events", service.jobEvents)
	mux.HandleFunc("GET /api/jobs/{id}/download", service.download)
	mux.Handle("/", spaHandler(dist))
	return securityHeaders(mux), nil
}

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	extractor, ffmpeg := a.tool.Available()
	status := "ok"
	if !extractor {
		status = "missing-dependency"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "sacdExtract": extractor, "ffmpeg": ffmpeg})
}

func (a *api) createScan(w http.ResponseWriter, r *http.Request) {
	extractor, _ := a.tool.Available()
	if !extractor {
		writeError(w, http.StatusServiceUnavailable, "sacd_extract is not installed. Run scripts/setup.sh --check and set SACD_EXTRACT_PATH if it is installed outside PATH.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the ISO upload: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["iso"]
	if len(files) != 1 {
		writeError(w, http.StatusBadRequest, "Choose exactly one SACD ISO file.")
		return
	}
	header := files[0]
	filename := filepath.Base(header.Filename)
	if !strings.EqualFold(filepath.Ext(filename), ".iso") {
		writeError(w, http.StatusBadRequest, "The selected file must have the .iso extension.")
		return
	}
	artworkFiles := r.MultipartForm.File["artwork"]
	if len(artworkFiles) > 1 {
		writeError(w, http.StatusBadRequest, "Choose at most one album image.")
		return
	}
	tempDir, err := os.MkdirTemp("", "iso-to-album-scan-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a temporary scan folder.")
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tempDir)
		}
	}()
	input, err := header.Open()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the selected ISO.")
		return
	}
	inputPath := filepath.Join(tempDir, "source.iso")
	output, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		input.Close()
		writeError(w, http.StatusInternalServerError, "Could not save the ISO for local processing.")
		return
	}
	size, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	input.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not finish saving the ISO.")
		return
	}
	var artworkName string
	var artworkInput io.ReadCloser
	if len(artworkFiles) == 1 {
		artworkName = filepath.Base(artworkFiles[0].Filename)
		if !supportedArtworkName(artworkName) {
			writeError(w, http.StatusBadRequest, "The album image must be a JPG, PNG, or WebP file.")
			return
		}
		artworkInput, err = artworkFiles[0].Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "Could not read the selected album image.")
			return
		}
		defer artworkInput.Close()
	}
	snapshot, err := a.manager.Inspect(r.Context(), tempDir, inputPath, filename, size, artworkName, artworkInput)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	keep = true
	writeJSON(w, http.StatusCreated, snapshot)
}

func (a *api) scanArtwork(w http.ResponseWriter, r *http.Request) {
	filePath, filename, ok := a.manager.Artwork(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Artwork not found.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, filePath)
}

func (a *api) replaceArtwork(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 26<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the album image: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["artwork"]
	if len(files) != 1 {
		writeError(w, http.StatusBadRequest, "Choose exactly one album image.")
		return
	}
	filename := filepath.Base(files[0].Filename)
	if !supportedArtworkName(filename) {
		writeError(w, http.StatusBadRequest, "The album image must be a JPG, PNG, or WebP file.")
		return
	}
	input, err := files[0].Open()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the selected album image.")
		return
	}
	defer input.Close()
	snapshot, err := a.manager.UpdateArtwork(r.PathValue("id"), filename, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *api) createJob(w http.ResponseWriter, r *http.Request) {
	var manifest jobs.Manifest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		writeError(w, http.StatusBadRequest, "The export settings are invalid: "+err.Error())
		return
	}
	_, ffmpeg := a.tool.Available()
	if (manifest.Format == "wav" || manifest.Format == "flac") && !ffmpeg {
		writeError(w, http.StatusServiceUnavailable, "FFmpeg is required for WAV conversion and FLAC artwork embedding.")
		return
	}
	snapshot, err := a.manager.Create(manifest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, snapshot)
}

func supportedArtworkName(filename string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func (a *api) jobStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.manager.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Job not found.")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *api) jobEvents(w http.ResponseWriter, r *http.Request) {
	updates, cancel, ok := a.manager.Subscribe(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Job not found.")
		return
	}
	defer cancel()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming is not supported by this server.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case snapshot, open := <-updates:
			if !open {
				return
			}
			data, _ := json.Marshal(snapshot)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			if snapshot.State == "complete" || snapshot.State == "error" {
				return
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func (a *api) download(w http.ResponseWriter, r *http.Request) {
	filePath, filename, ok := a.manager.Download(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "The ZIP is not ready or this job writes to a server folder.")
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, filePath)
}

func spaHandler(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requested == "." || requested == "" || requested == "index.html" {
			serveIndex(w, dist)
			return
		}
		if info, err := fs.Stat(dist, requested); err == nil && !info.IsDir() {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/" + requested
			files.ServeHTTP(w, clone)
			return
		}
		serveIndex(w, dist)
	})
}

func serveIndex(w http.ResponseWriter, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "embedded frontend is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
