package artwork

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	id3v2 "github.com/bogem/id3v2/v2"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

const (
	MaxDimension  = 1000
	maxSourceSize = 25 << 20
	maxSourceArea = 50_000_000
)

type Details struct {
	Width  int
	Height int
}

func Normalize(source io.Reader, target string) (Details, error) {
	data, err := io.ReadAll(io.LimitReader(source, maxSourceSize+1))
	if err != nil {
		return Details{}, fmt.Errorf("read artwork: %w", err)
	}
	if len(data) > maxSourceSize {
		return Details{}, fmt.Errorf("artwork is larger than 25 MB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Details{}, fmt.Errorf("decode artwork: use a valid JPG, PNG, or WebP image")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxSourceArea {
		return Details{}, fmt.Errorf("artwork dimensions are not supported")
	}
	sourceImage, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Details{}, fmt.Errorf("decode artwork: %w", err)
	}

	width, height := scaledSize(config.Width, config.Height)
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), sourceImage, sourceImage.Bounds(), draw.Src, nil)
	if err := writeJPEG(target, destination); err != nil {
		return Details{}, err
	}
	return Details{Width: width, Height: height}, nil
}

func Generate(album, artist, target string) (Details, error) {
	album = strings.TrimSpace(album)
	if album == "" {
		album = "Untitled album"
	}
	artist = strings.TrimSpace(artist)
	if artist == "" {
		artist = "Unknown artist"
	}

	canvas := image.NewRGBA(image.Rect(0, 0, MaxDimension, MaxDimension))
	seed := hashText(album + "\x00" + artist)
	start := color.RGBA{R: uint8(185 + seed%38), G: uint8(218 + (seed>>8)%28), B: uint8(231 + (seed>>16)%22), A: 255}
	end := color.RGBA{R: uint8(59 + (seed>>4)%54), G: uint8(103 + (seed>>12)%54), B: uint8(127 + (seed>>20)%54), A: 255}
	for y := 0; y < MaxDimension; y++ {
		for x := 0; x < MaxDimension; x++ {
			t := (float64(x) + float64(y)*0.72) / (float64(MaxDimension) * 1.72)
			canvas.SetRGBA(x, y, blend(start, end, t))
		}
	}

	panel := image.Rect(84, 84, 916, 916)
	draw.Draw(canvas, panel, &image.Uniform{C: color.RGBA{R: 248, G: 252, B: 254, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(84, 84, 916, 91), &image.Uniform{C: color.RGBA{R: 169, G: 213, B: 231, A: 255}}, image.Point{}, draw.Src)

	labelFace, err := makeFace(gobold.TTF, 25)
	if err != nil {
		return Details{}, err
	}
	titleFace, err := makeFace(gobold.TTF, 66)
	if err != nil {
		return Details{}, err
	}
	artistFace, err := makeFace(goregular.TTF, 33)
	if err != nil {
		return Details{}, err
	}
	defer closeFace(labelFace)
	defer closeFace(titleFace)
	defer closeFace(artistFace)

	drawText(canvas, labelFace, "SACD · ISO TO ALBUM", 126, 158, color.RGBA{R: 82, G: 120, B: 139, A: 255})
	lines := wrap(album, titleFace, 742, 4)
	y := 342
	for _, line := range lines {
		drawText(canvas, titleFace, line, 126, y, color.RGBA{R: 54, G: 79, B: 91, A: 255})
		y += 82
	}
	draw.Draw(canvas, image.Rect(126, 770, 874, 772), &image.Uniform{C: color.RGBA{R: 201, G: 229, B: 241, A: 255}}, image.Point{}, draw.Src)
	artistLine := truncate(artist, artistFace, 742)
	drawText(canvas, artistFace, artistLine, 126, 838, color.RGBA{R: 82, G: 120, B: 139, A: 255})

	if err := writeJPEG(target, canvas); err != nil {
		return Details{}, err
	}
	return Details{Width: MaxDimension, Height: MaxDimension}, nil
}

func CopyToAlbumFolders(root, coverPath string) error {
	directories := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isAudioFile(entry.Name()) {
			return nil
		}
		directories[filepath.Dir(path)] = struct{}{}
		return nil
	})
	if err != nil {
		return fmt.Errorf("find album folders: %w", err)
	}
	if len(directories) == 0 {
		return fmt.Errorf("the extractor produced no supported audio files")
	}
	directories[root] = struct{}{}
	for directory := range directories {
		if err := copyFile(coverPath, filepath.Join(directory, "cover.jpg")); err != nil {
			return fmt.Errorf("write album cover: %w", err)
		}
	}
	return nil
}

func EmbedDSFFolder(root, coverPath string) error {
	cover, err := os.ReadFile(coverPath)
	if err != nil {
		return fmt.Errorf("read canonical cover: %w", err)
	}
	files, err := filesWithExtension(root, ".dsf")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("the extractor produced no DSF files")
	}
	for _, path := range files {
		if err := EmbedDSF(path, cover); err != nil {
			return fmt.Errorf("embed cover in %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func EmbedDSF(path string, cover []byte) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	header := make([]byte, 28)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("read DSF header: %w", err)
	}
	if string(header[:4]) != "DSD " || binary.LittleEndian.Uint64(header[4:12]) < 28 {
		return fmt.Errorf("invalid DSF header")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	metadataOffset := int64(binary.LittleEndian.Uint64(header[20:28]))
	if metadataOffset != 0 && (metadataOffset < 28 || metadataOffset > info.Size()) {
		return fmt.Errorf("invalid DSF metadata pointer")
	}

	tag := id3v2.NewEmptyTag()
	writeOffset := info.Size()
	if metadataOffset != 0 {
		writeOffset = metadataOffset
		if _, err := file.Seek(metadataOffset, io.SeekStart); err != nil {
			return err
		}
		tag, err = id3v2.ParseReader(io.LimitReader(file, info.Size()-metadataOffset), id3v2.Options{Parse: true})
		if err != nil {
			return fmt.Errorf("parse existing ID3 metadata: %w", err)
		}
	}
	tag.DeleteFrames(tag.CommonID("Attached picture"))
	tag.AddAttachedPicture(id3v2.PictureFrame{
		Encoding:    id3v2.EncodingUTF8,
		MimeType:    "image/jpeg",
		PictureType: id3v2.PTFrontCover,
		Description: "Front cover",
		Picture:     cover,
	})
	var metadata bytes.Buffer
	if _, err := tag.WriteTo(&metadata); err != nil {
		return fmt.Errorf("encode ID3 metadata: %w", err)
	}
	if metadata.Len() == 0 {
		return fmt.Errorf("encode ID3 metadata: empty tag")
	}
	if err := file.Truncate(writeOffset); err != nil {
		return err
	}
	if _, err := file.Seek(writeOffset, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(metadata.Bytes()); err != nil {
		return err
	}
	newSize := writeOffset + int64(metadata.Len())
	binary.LittleEndian.PutUint64(header[12:20], uint64(newSize))
	binary.LittleEndian.PutUint64(header[20:28], uint64(writeOffset))
	if _, err := file.Seek(12, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(header[12:28]); err != nil {
		return err
	}
	return file.Sync()
}

func EmbedFLACFolder(ctx context.Context, ffmpegPath, root, coverPath string) error {
	files, err := filesWithExtension(root, ".flac")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("the extractor produced no FLAC files")
	}
	for _, input := range files {
		temporary := input + ".artwork.flac"
		command := exec.CommandContext(ctx, ffmpegPath,
			"-hide_banner", "-loglevel", "error", "-y",
			"-i", input, "-i", coverPath,
			"-map", "0:a", "-map", "1:v",
			"-map_metadata", "0", "-c:a", "copy", "-c:v", "copy",
			"-disposition:v:0", "attached_pic",
			"-metadata:s:v:0", "title=Album cover",
			"-metadata:s:v:0", "comment=Cover (front)",
			temporary,
		)
		if output, err := command.CombinedOutput(); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("embed cover in %s: %w: %s", filepath.Base(input), err, strings.TrimSpace(string(output)))
		}
		if err := replaceFile(temporary, input); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace %s after artwork embedding: %w", filepath.Base(input), err)
		}
	}
	return nil
}

func scaledSize(width, height int) (int, int) {
	if width <= MaxDimension && height <= MaxDimension {
		return width, height
	}
	scale := math.Min(float64(MaxDimension)/float64(width), float64(MaxDimension)/float64(height))
	return max(1, int(math.Round(float64(width)*scale))), max(1, int(math.Round(float64(height)*scale)))
}

func writeJPEG(target string, value image.Image) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create artwork directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".cover-*.jpg")
	if err != nil {
		return fmt.Errorf("create artwork file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := jpeg.Encode(temporary, value, &jpeg.Options{Quality: 90}); err != nil {
		temporary.Close()
		return fmt.Errorf("encode artwork: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("finish artwork: %w", err)
	}
	if err := replaceFile(temporaryName, target); err != nil {
		return fmt.Errorf("save artwork: %w", err)
	}
	return nil
}

func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, target)
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func filesWithExtension(root, extension string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), extension) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func isAudioFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".dsf", ".dff", ".wav", ".flac":
		return true
	default:
		return false
	}
}

func makeFace(data []byte, size float64) (font.Face, error) {
	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse cover font: %w", err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("create cover font: %w", err)
	}
	return face, nil
}

func closeFace(face font.Face) {
	if closer, ok := face.(io.Closer); ok {
		_ = closer.Close()
	}
}

func drawText(destination draw.Image, face font.Face, value string, x, baseline int, ink color.Color) {
	drawer := font.Drawer{Dst: destination, Src: image.NewUniform(ink), Face: face, Dot: fixedPoint(x, baseline)}
	drawer.DrawString(value)
}

func fixedPoint(x, y int) fixed.Point26_6 {
	return fixed.P(x, y)
}

func wrap(value string, face font.Face, width, maximumLines int) []string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{"Untitled album"}
	}
	lines := make([]string, 0, maximumLines)
	current := ""
	for index, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if measure(face, candidate) <= width || current == "" {
			current = candidate
		} else {
			lines = append(lines, truncate(current, face, width))
			current = word
		}
		if len(lines) == maximumLines-1 && index < len(words)-1 {
			current += " " + strings.Join(words[index+1:], " ")
			break
		}
	}
	if current != "" && len(lines) < maximumLines {
		lines = append(lines, truncate(current, face, width))
	}
	return lines
}

func truncate(value string, face font.Face, width int) string {
	if measure(face, value) <= width {
		return value
	}
	result := strings.TrimSpace(value)
	for result != "" && measure(face, result+"…") > width {
		_, size := utf8.DecodeLastRuneInString(result)
		result = strings.TrimSpace(result[:len(result)-size])
	}
	return result + "…"
}

func measure(face font.Face, value string) int {
	return font.MeasureString(face, value).Ceil()
}

func blend(start, end color.RGBA, amount float64) color.RGBA {
	amount = math.Max(0, math.Min(1, amount))
	return color.RGBA{
		R: uint8(float64(start.R)*(1-amount) + float64(end.R)*amount),
		G: uint8(float64(start.G)*(1-amount) + float64(end.G)*amount),
		B: uint8(float64(start.B)*(1-amount) + float64(end.B)*amount),
		A: 255,
	}
}

func hashText(value string) uint32 {
	var result uint32 = 2166136261
	for _, next := range []byte(value) {
		result ^= uint32(next)
		result *= 16777619
	}
	return result
}
