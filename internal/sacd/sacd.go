package sacd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Area struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Channels   int    `json:"channels"`
	Encoding   string `json:"encoding"`
	TrackCount int    `json:"trackCount"`
}

type Track struct {
	Index           int      `json:"index"`
	Title           string   `json:"title"`
	Performer       string   `json:"performer,omitempty"`
	Duration        string   `json:"duration"`
	DurationSeconds float64  `json:"durationSeconds"`
	Areas           []string `json:"areas"`
}

type DiscInfo struct {
	Album  string  `json:"album"`
	Artist string  `json:"artist"`
	Areas  []Area  `json:"areas"`
	Tracks []Track `json:"tracks"`
	DST    bool    `json:"dst"`
}

type ExtractOptions struct {
	Format      string
	Area        string
	PCMRate     int
	PreserveDST bool
}

type Progress struct {
	Current int
	Total   int
	Value   float64
	Message string
}

type Tool struct {
	ExtractPath string
	FFmpegPath  string
}

func NewTool(extractPath, ffmpegPath string) *Tool {
	return &Tool{ExtractPath: extractPath, FFmpegPath: ffmpegPath}
}

func (t *Tool) Available() (bool, bool) {
	return toolAvailable(t.ExtractPath), toolAvailable(t.FFmpegPath)
}

func toolAvailable(path string) bool {
	if strings.ContainsRune(path, filepath.Separator) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}

func (t *Tool) Inspect(ctx context.Context, input string) (DiscInfo, error) {
	command := exec.CommandContext(ctx, t.ExtractPath, input, "--info")
	output, err := command.CombinedOutput()
	if err != nil {
		return DiscInfo{}, fmt.Errorf("read SACD table of contents: %w: %s", err, cleanToolError(output))
	}
	info, err := ParseInfo(string(output))
	if err != nil {
		return DiscInfo{}, fmt.Errorf("parse SACD table of contents: %w", err)
	}
	return info, nil
}

func (t *Tool) Extract(ctx context.Context, input, outputDir string, options ExtractOptions, onProgress func(Progress)) error {
	args, err := extractArgs(input, outputDir, options)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, t.ExtractPath, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("read sacd_extract output: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start sacd_extract: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Split(splitToolLines)
	current, total := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if match := processingPattern.FindStringSubmatch(line); match != nil {
			current, _ = strconv.Atoi(match[1])
			total, _ = strconv.Atoi(match[2])
			onProgress(Progress{Current: current, Total: total, Message: fmt.Sprintf("Extracting track %d of %d.", current, total)})
			continue
		}
		if match := completedPattern.FindStringSubmatch(line); match != nil {
			value, _ := strconv.Atoi(match[1])
			onProgress(Progress{Current: current, Total: total, Value: float64(value) / 100, Message: line})
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("read sacd_extract progress: %w", scanErr)
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("sacd_extract failed: %w: %s", err, cleanToolError(stderr.Bytes()))
	}
	return nil
}

func (t *Tool) ConvertFLACToWAV(ctx context.Context, root string, onFile func(current, total int, name string)) error {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".flac") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("find FLAC intermediates: %w", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("sacd_extract produced no FLAC files for WAV conversion")
	}
	for index, input := range files {
		onFile(index+1, len(files), filepath.Base(input))
		output := strings.TrimSuffix(input, filepath.Ext(input)) + ".wav"
		command := exec.CommandContext(ctx, t.FFmpegPath, "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-map_metadata", "0", "-c:a", "pcm_s24le", output)
		if data, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("convert %s to WAV: %w: %s", filepath.Base(input), err, cleanToolError(data))
		}
		if err := os.Remove(input); err != nil {
			return fmt.Errorf("remove FLAC intermediate: %w", err)
		}
	}
	return nil
}

func extractArgs(input, outputDir string, options ExtractOptions) ([]string, error) {
	args := []string{"--no-log"}
	switch options.Area {
	case "stereo":
		args = append(args, "--stereo")
	case "multichannel":
		args = append(args, "--multichannel")
	case "both":
		args = append(args, "--stereo", "--multichannel")
	default:
		return nil, fmt.Errorf("unsupported SACD area %q", options.Area)
	}
	switch options.Format {
	case "dsf":
		args = append(args, "--dsf", "--convert-dst")
	case "dff":
		args = append(args, "--dsdiff")
		if !options.PreserveDST {
			args = append(args, "--convert-dst")
		}
	case "flac", "wav":
		if options.PCMRate != 88200 && options.PCMRate != 176400 && options.PCMRate != 352800 {
			return nil, fmt.Errorf("unsupported PCM sample rate %d", options.PCMRate)
		}
		args = append(args, "--flac", "--flac-rate", strconv.Itoa(options.PCMRate))
	default:
		return nil, fmt.Errorf("unsupported output format %q", options.Format)
	}
	args = append(args, "--track-output-dir", outputDir, input)
	return args, nil
}

var (
	areaPattern       = regexp.MustCompile(`Area Information \[(\d+)\]:`)
	trackCountPattern = regexp.MustCompile(`Track Count:\s*(\d+)`)
	encodingPattern   = regexp.MustCompile(`Frame format encoding:\s*\[[^]]*\]\s*\[([^]]+)\]`)
	channelsPattern   = regexp.MustCompile(`Speaker config:\s*(\d+)\s*Channel`)
	titlePattern      = regexp.MustCompile(`Title\[(\d+)\]:\s*(.*)`)
	performerPattern  = regexp.MustCompile(`Performer\[(\d+)\]:\s*(.*)`)
	durationPattern   = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+)`)
	processingPattern = regexp.MustCompile(`\((\d+)/(\d+)\)`)
	completedPattern  = regexp.MustCompile(`Completed:\s*(\d+)%`)
)

type parsedArea struct {
	Area
	titles     map[int]string
	performers map[int]string
	durations  map[int]Track
	nextTime   int
}

func ParseInfo(output string) (DiscInfo, error) {
	info := DiscInfo{}
	section := ""
	var current *parsedArea
	var areas []*parsedArea
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch line {
		case "Album Information:":
			section = "album"
			continue
		case "Disc Information:":
			section = "disc"
			continue
		}
		if match := areaPattern.FindStringSubmatch(line); match != nil {
			current = &parsedArea{titles: map[int]string{}, performers: map[int]string{}, durations: map[int]Track{}}
			areas = append(areas, current)
			section = "area"
			continue
		}
		if current == nil && section == "album" {
			if strings.HasPrefix(line, "Title:") && info.Album == "" {
				info.Album = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
			}
			if strings.HasPrefix(line, "Artist:") && info.Artist == "" {
				info.Artist = strings.TrimSpace(strings.TrimPrefix(line, "Artist:"))
			}
			continue
		}
		if current == nil {
			continue
		}
		if match := trackCountPattern.FindStringSubmatch(line); match != nil {
			current.TrackCount, _ = strconv.Atoi(match[1])
			continue
		}
		if match := encodingPattern.FindStringSubmatch(line); match != nil {
			current.Encoding = strings.TrimSpace(match[1])
			if strings.Contains(strings.ToUpper(current.Encoding), "DST") {
				info.DST = true
			}
			continue
		}
		if match := channelsPattern.FindStringSubmatch(line); match != nil {
			current.Channels, _ = strconv.Atoi(match[1])
			if current.Channels == 2 {
				current.ID, current.Label = "stereo", "Stereo"
			} else {
				current.ID, current.Label = "multichannel", fmt.Sprintf("Multichannel (%dch)", current.Channels)
			}
			continue
		}
		if match := titlePattern.FindStringSubmatch(line); match != nil {
			index, _ := strconv.Atoi(match[1])
			current.titles[index-1] = strings.TrimSpace(match[2])
			continue
		}
		if match := performerPattern.FindStringSubmatch(line); match != nil {
			index, _ := strconv.Atoi(match[1])
			current.performers[index-1] = strings.TrimSpace(match[2])
			continue
		}
		if match := durationPattern.FindStringSubmatch(line); match != nil {
			minutes, _ := strconv.Atoi(match[1])
			seconds, _ := strconv.Atoi(match[2])
			frames, _ := strconv.Atoi(match[3])
			index := current.nextTime
			current.nextTime++
			current.durations[index] = Track{Duration: fmt.Sprintf("%02d:%02d", minutes, seconds), DurationSeconds: float64(minutes*60+seconds) + float64(frames)/75}
		}
	}
	if len(areas) == 0 {
		return DiscInfo{}, fmt.Errorf("no SACD audio areas were reported")
	}
	maxTracks := 0
	for _, area := range areas {
		if area.ID == "" {
			area.ID, area.Label = fmt.Sprintf("area-%d", len(info.Areas)+1), "Unknown area"
		}
		if area.Encoding == "" {
			area.Encoding = "DSD"
		}
		if area.TrackCount > maxTracks {
			maxTracks = area.TrackCount
		}
		info.Areas = append(info.Areas, area.Area)
	}
	if maxTracks == 0 {
		return DiscInfo{}, fmt.Errorf("the SACD areas contain no tracks")
	}
	for index := 0; index < maxTracks; index++ {
		track := Track{Index: index, Title: fmt.Sprintf("Track %02d", index+1), Duration: "--:--"}
		for _, area := range areas {
			if index >= area.TrackCount {
				continue
			}
			track.Areas = append(track.Areas, area.ID)
			if title := area.titles[index]; title != "" && strings.HasPrefix(track.Title, "Track ") {
				track.Title = title
			}
			if performer := area.performers[index]; performer != "" && track.Performer == "" {
				track.Performer = performer
			}
			if duration, ok := area.durations[index]; ok && track.Duration == "--:--" {
				track.Duration, track.DurationSeconds = duration.Duration, duration.DurationSeconds
			}
		}
		info.Tracks = append(info.Tracks, track)
	}
	if info.Album == "" {
		info.Album = "Untitled SACD"
	}
	return info, nil
}

func splitToolLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexAny(data, "\r\n"); index >= 0 {
		advance = index + 1
		for advance < len(data) && (data[advance] == '\r' || data[advance] == '\n') {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func cleanToolError(data []byte) string {
	value := strings.Join(strings.Fields(string(data)), " ")
	if len(value) > 500 {
		return value[:500] + "…"
	}
	if value == "" {
		return "no diagnostic output"
	}
	return value
}

func CopyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}
