package main

import (
	"encoding/binary"
	"image"
)

// EXIF handling, in the smallest amount of code that covers what cpwo needs:
// read the orientation tag so decoded pixels can be turned the right way up,
// and carry the whole APP1 segment across to a re-encoded JPEG when -exif asks
// for it. Anything more (writing EXIF into WebP or AVIF, ICC profiles, XMP)
// means container surgery the encoders do not expose, so cpwo does not pretend
// to do it.

const exifOrientationTag = 0x0112

// jpegExif returns the raw APP1 payload of a JPEG, "Exif\0\0" header and all,
// or nil when there is none. Segments after the start of scan are picture data,
// not metadata, so the search stops there.
func jpegExif(b []byte) []byte {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil // not a JPEG
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return nil // out of step with the segment chain; give up quietly
		}
		m := b[i+1]
		if m == 0xD8 || (m >= 0xD0 && m <= 0xD7) || m == 0x01 {
			i += 2 // standalone marker, no length word
			continue
		}
		if m == 0xDA || m == 0xD9 {
			return nil // SOS or EOI: no metadata left
		}
		n := int(b[i+2])<<8 | int(b[i+3])
		if n < 2 || i+2+n > len(b) {
			return nil
		}
		if m == 0xE1 {
			p := b[i+4 : i+2+n]
			if len(p) > 6 && string(p[:6]) == "Exif\x00\x00" {
				return p
			}
		}
		i += 2 + n
	}
	return nil
}

// exifOrientationAt locates the IFD0 orientation entry inside an APP1 payload,
// returning the offset of its 2-byte value and the byte order to read it with.
// TIFF stores a value shorter than four bytes left-justified in the entry's
// value field, so a SHORT lands on the first two.
func exifOrientationAt(app1 []byte) (int, binary.ByteOrder, bool) {
	if len(app1) < 6+8 {
		return 0, nil, false
	}
	tiff := app1[6:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, nil, false
	}
	if order.Uint16(tiff[2:]) != 0x002A {
		return 0, nil, false
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0, nil, false
	}
	count := int(order.Uint16(tiff[ifd:]))
	for e := 0; e < count; e++ {
		off := ifd + 2 + e*12
		if off+12 > len(tiff) {
			return 0, nil, false
		}
		if order.Uint16(tiff[off:]) == exifOrientationTag {
			return 6 + off + 8, order, true // +8: past tag, type and count
		}
	}
	return 0, nil, false
}

// exifOrientation is the tag's value, or 1 (upright) when there is no readable
// tag. Anything outside 1-8 is a broken file and is treated as upright.
func exifOrientation(app1 []byte) int {
	off, order, ok := exifOrientationAt(app1)
	if !ok {
		return 1
	}
	v := int(order.Uint16(app1[off:]))
	if v < 1 || v > 8 {
		return 1
	}
	return v
}

// exifUpright copies app1 with the orientation tag reset to 1. cpwo bakes the
// rotation into the pixels it writes, so carrying the original tag through
// would make a viewer turn the image a second time.
func exifUpright(app1 []byte) []byte {
	out := make([]byte, len(app1))
	copy(out, app1)
	if off, order, ok := exifOrientationAt(out); ok {
		order.PutUint16(out[off:], 1)
	}
	return out
}

// withExif splices an APP1 segment into a freshly encoded JPEG, straight after
// the SOI marker, which is where a decoder expects to find it.
func withExif(jpg, app1 []byte) []byte {
	if len(app1) == 0 || len(jpg) < 2 || app1[0] == 0 {
		return jpg
	}
	n := len(app1) + 2
	if n > 0xFFFF {
		return jpg // will not fit in one segment; better no EXIF than a corrupt file
	}
	out := make([]byte, 0, len(jpg)+n+2)
	out = append(out, jpg[:2]...) // SOI
	out = append(out, 0xFF, 0xE1, byte(n>>8), byte(n))
	out = append(out, app1...)
	return append(out, jpg[2:]...)
}

// orient turns a decoded image the right way up for an EXIF orientation. The
// eight cases are the four rotations and their mirrors; 5 to 8 also swap the
// axes, so the result is taller than it is wide and vice versa.
//
// ponytail: goes through At/Set rather than the concrete pixel buffer, so it
// costs an interface call per pixel. It only runs on images that are actually
// rotated, and the encoders that follow cost far more; specialise on
// *image.YCbCr if a folder of turned photos ever feels slow.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // mirrored along the main diagonal
				dx, dy = y, x
			case 6: // 90 clockwise
				dx, dy = h-1-y, x
			case 7: // mirrored along the other diagonal
				dx, dy = h-1-y, w-1-x
			case 8: // 90 anticlockwise
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
