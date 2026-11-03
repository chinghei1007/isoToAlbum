package artwork

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	id3v2 "github.com/bogem/id3v2/v2"
)

func TestNormalizeCreatesBoundedJPEG(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1600; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 180, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "cover.jpg")
	details, err := Normalize(&encoded, target)
	if err != nil {
		t.Fatal(err)
	}
	if details.Width != 1000 || details.Height != 500 {
		t.Fatalf("unexpected normalized size: %#v", details)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 1000 || decoded.Bounds().Dy() != 500 {
		t.Fatalf("unexpected JPEG bounds: %v", decoded.Bounds())
	}
}

func TestGenerateCreatesTitleCover(t *testing.T) {
	target := filepath.Join(t.TempDir(), "cover.jpg")
	details, err := Generate("Kind of Blue", "Miles Davis", target)
	if err != nil {
		t.Fatal(err)
	}
	if details.Width != MaxDimension || details.Height != MaxDimension {
		t.Fatalf("unexpected generated size: %#v", details)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := jpeg.Decode(file); err != nil {
		t.Fatalf("generated cover is not JPEG: %v", err)
	}
}

func TestEmbedDSFPreservesAudioAndAddsOneFrontCover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.dsf")
	audio := []byte("lossless-dsd-audio-payload")
	header := make([]byte, 28)
	copy(header, "DSD ")
	binary.LittleEndian.PutUint64(header[4:12], 28)
	binary.LittleEndian.PutUint64(header[12:20], uint64(len(header)+len(audio)))
	if err := os.WriteFile(path, append(header, audio...), 0o644); err != nil {
		t.Fatal(err)
	}
	cover := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := EmbedDSF(path, cover); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := binary.LittleEndian.Uint64(data[20:28])
	if offset != uint64(len(header)+len(audio)) {
		t.Fatalf("unexpected metadata offset: %d", offset)
	}
	if !bytes.Equal(data[28:offset], audio) {
		t.Fatal("DSD audio payload changed while adding artwork")
	}
	tag, err := id3v2.ParseReader(bytes.NewReader(data[offset:]), id3v2.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	frames := tag.GetFrames(tag.CommonID("Attached picture"))
	if len(frames) != 1 {
		t.Fatalf("expected one picture frame, got %d", len(frames))
	}
	picture, ok := frames[0].(id3v2.PictureFrame)
	if !ok || picture.PictureType != id3v2.PTFrontCover || !bytes.Equal(picture.Picture, cover) {
		t.Fatalf("unexpected picture frame: %#v", frames[0])
	}
}
