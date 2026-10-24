package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// app1Fixture is a minimal but real APP1 segment: little-endian TIFF, one IFD0
// entry, the orientation tag set to o.
func app1Fixture(o byte) []byte {
	return []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, // byte order, magic
		0x08, 0x00, 0x00, 0x00, // IFD0 starts at offset 8
		0x01, 0x00, // one entry
		0x12, 0x01, // tag 0x0112, Orientation
		0x03, 0x00, // type SHORT
		0x01, 0x00, 0x00, 0x00, // count 1
		o, 0x00, 0x00, 0x00, // value, left-justified in the field
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
}

// writeJPEGWithExif writes a wide, photo-like JPEG carrying orientation o.
func writeJPEGWithExif(t *testing.T, path string, w, h int, o byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 3 % 256), uint8(y * 5 % 256), uint8((x + y) % 256), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 96}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, withExif(buf.Bytes(), app1Fixture(o)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExifSegment covers the three things cpwo asks of an APP1 segment: find
// it, read the orientation out of it, and hand back a copy that says upright.
func TestExifSegment(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()

	if got := jpegExif(plain); got != nil {
		t.Errorf("found %d bytes of EXIF in a JPEG that has none", len(got))
	}
	if got := exifOrientation(nil); got != 1 {
		t.Errorf("orientation of a JPEG without EXIF = %d, want 1 (upright)", got)
	}

	withIt := withExif(plain, app1Fixture(6))
	app1 := jpegExif(withIt)
	if app1 == nil {
		t.Fatal("spliced an APP1 segment in and could not read it back")
	}
	if got := exifOrientation(app1); got != 6 {
		t.Errorf("orientation = %d, want 6", got)
	}
	if got := exifOrientation(exifUpright(app1)); got != 1 {
		t.Errorf("orientation after exifUpright = %d, want 1", got)
	}
	if exifOrientation(app1) != 6 {
		t.Error("exifUpright wrote through to the segment it was given; it must copy")
	}
	// The splice must leave a decodable JPEG behind, not just parseable bytes.
	if _, err := jpeg.Decode(bytes.NewReader(withIt)); err != nil {
		t.Errorf("JPEG no longer decodes after the EXIF splice: %v", err)
	}
}

// TestOrient checks the axis swap and that a known corner lands where the EXIF
// spec says it should. Orientation 6 is a phone held upright, by far the most
// common one that is not 1.
func TestOrient(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.Set(0, 0, color.RGBA{1, 2, 3, 255}) // top-left marker

	got := orient(src, 6) // 90 clockwise
	if b := got.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Errorf("orient(4x2, 6) is %dx%d, want 2x4", b.Dx(), b.Dy())
	}
	if r, g, b, _ := got.At(1, 0).RGBA(); r>>8 != 1 || g>>8 != 2 || b>>8 != 3 {
		t.Error("rotating 90 clockwise should put the top-left pixel top-right")
	}
	if same := orient(src, 1); same != image.Image(src) {
		t.Error("orientation 1 should hand back the same image untouched")
	}
	for _, o := range []int{5, 6, 7, 8} {
		if b := orient(src, o).Bounds(); b.Dx() != 2 || b.Dy() != 4 {
			t.Errorf("orientation %d did not swap the axes", o)
		}
	}
	for _, o := range []int{2, 3, 4} {
		if b := orient(src, o).Bounds(); b.Dx() != 4 || b.Dy() != 2 {
			t.Errorf("orientation %d swapped the axes and should not have", o)
		}
	}
}

// TestExifOrientationPipeline is the bug this all exists for: a photo tagged
// orientation 6 used to come out of cpwo on its side, because image.Decode
// ignores the tag and no encoder writes one back. Every output must now be
// turned the right way up.
func TestExifOrientationPipeline(t *testing.T) {
	src := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(src, "photo.jpg"), 256, 128, 6)

	o := options{quality: 82, jobs: 2, on: map[string]bool{"img": true, "webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"photo.min.jpg", "photo.webp"} {
		cfg := decodeConfig(t, filepath.Join(src, name))
		if cfg.Width != 128 || cfg.Height != 256 {
			t.Errorf("%s is %dx%d, want 128x256: the EXIF rotation was not applied",
				name, cfg.Width, cfg.Height)
		}
	}
	// Stripping is the default, and it has to stay that way or the rotation
	// above would be applied a second time by the viewer.
	if app1 := jpegExif(read(t, filepath.Join(src, "photo.min.jpg"))); app1 != nil {
		t.Error("EXIF survived into photo.min.jpg without -exif")
	}
}

// TestExifKeep: -exif carries the segment across, with the orientation neutered
// so a viewer does not turn pixels cpwo has already turned.
func TestExifKeep(t *testing.T) {
	src := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(src, "photo.jpg"), 256, 128, 6)

	o := options{quality: 82, jobs: 2, exif: true, on: map[string]bool{"img": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(src, "photo.min.jpg")
	app1 := jpegExif(read(t, out))
	if app1 == nil {
		t.Fatal("-exif did not carry the APP1 segment across")
	}
	if got := exifOrientation(app1); got != 1 {
		t.Errorf("kept orientation %d, want 1: cpwo already turned the pixels", got)
	}
	if cfg := decodeConfig(t, out); cfg.Width != 128 || cfg.Height != 256 {
		t.Errorf("photo.min.jpg is %dx%d, want 128x256", cfg.Width, cfg.Height)
	}
}

func decodeConfig(t *testing.T, path string) image.Config {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
