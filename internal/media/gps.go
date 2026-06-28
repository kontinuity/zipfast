package media

import (
	"bytes"
	"fmt"
	"os"
)

// JPEG marker bytes used while walking the segment structure.
const (
	jpegMarkerPrefix = 0xFF
	markerSOI        = 0xD8 // Start Of Image
	markerEOI        = 0xD9 // End Of Image
	markerSOS        = 0xDA // Start Of Scan (entropy-coded data follows; stop parsing)
	markerAPP1       = 0xE1 // APP1 — holds Exif (and thus GPS) metadata
)

// exifSignature is the identifier at the start of an APP1 payload that marks it
// as an Exif segment ("Exif\0\0"). We only drop APP1 segments that carry Exif so
// that other APP1 uses (e.g. XMP) are preserved.
var exifSignature = []byte{'E', 'x', 'i', 'f', 0x00, 0x00}

// StripGPS performs a best-effort removal of EXIF GPS data from the file at
// srcPath, rewriting it in place.
//
// For v1 the approach is deliberately simple and safe: if the file is a JPEG, we
// drop the entire Exif APP1 segment (which contains the GPS IFD). This removes
// GPS coordinates along with the rest of the Exif block. Non-JPEG inputs are left
// untouched.
//
// It returns:
//   - (true, nil)  an Exif segment was found and removed; srcPath was rewritten.
//   - (false, nil) nothing to do (not a JPEG, or no Exif segment present), or the
//     structure was anything we weren't fully confident about. In every "false"
//     case the original file is left byte-for-byte intact.
//   - (false, err) only for genuine I/O errors reading or writing the file.
//
// Safety first: the file is only overwritten after a new, validated byte slice
// has been fully assembled, so a parse that hits anything unexpected results in a
// no-op rather than a corrupted file.
func StripGPS(srcPath string) (removed bool, err error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return false, fmt.Errorf("media: read for gps strip: %w", err)
	}

	out, removed, ok := stripExifAPP1(data)
	if !ok || !removed {
		// Not a JPEG, no Exif segment, or we weren't confident parsing it. Leave
		// the file exactly as-is.
		return false, nil
	}

	// Rewrite atomically-ish: write to a temp file in the same directory and
	// rename over the original so a crash mid-write can't truncate the upload.
	tmp, err := os.CreateTemp(dirOf(srcPath), ".zipfast-gps-*")
	if err != nil {
		return false, fmt.Errorf("media: temp for gps strip: %w", err)
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(out); werr != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("media: write gps-stripped data: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("media: close gps temp: %w", cerr)
	}
	if rerr := os.Rename(tmpName, srcPath); rerr != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("media: replace original after gps strip: %w", rerr)
	}
	return true, nil
}

// stripExifAPP1 walks the JPEG marker segments in data and returns a copy with
// every Exif APP1 segment removed.
//
// Returns (out, removed, ok):
//   - ok=false  data is not a JPEG we can safely parse; out/removed are unset and
//     the caller must not modify the file.
//   - removed   whether at least one Exif APP1 segment was dropped.
//   - out       the rewritten bytes (only meaningful when ok && removed).
//
// The parser is conservative: it understands the standard marker framing up to
// the Start Of Scan, and bails out (ok=false) on anything malformed so we never
// emit a corrupt file.
func stripExifAPP1(data []byte) (out []byte, removed bool, ok bool) {
	// Must begin with SOI (FF D8).
	if len(data) < 2 || data[0] != jpegMarkerPrefix || data[1] != markerSOI {
		return nil, false, false
	}

	buf := bytes.NewBuffer(make([]byte, 0, len(data)))
	buf.Write(data[:2]) // copy SOI through

	i := 2
	for i+1 < len(data) {
		// Every marker starts with 0xFF. Padding 0xFF bytes are allowed between
		// segments and should be copied through verbatim.
		if data[i] != jpegMarkerPrefix {
			// Unexpected non-marker byte where a marker was expected: parse failed.
			return nil, false, false
		}
		// Skip any run of fill 0xFF bytes, copying them through.
		for i+1 < len(data) && data[i] == jpegMarkerPrefix && data[i+1] == jpegMarkerPrefix {
			buf.WriteByte(data[i])
			i++
		}
		if i+1 >= len(data) {
			return nil, false, false
		}

		marker := data[i+1]

		// Start Of Scan: the compressed image data follows and has no length
		// field we can hop over. Copy the rest of the file unchanged and stop.
		if marker == markerSOS {
			buf.Write(data[i:])
			return buf.Bytes(), removed, true
		}

		// EOI with no scan (unusual, but handle it): copy and finish.
		if marker == markerEOI {
			buf.Write(data[i:])
			return buf.Bytes(), removed, true
		}

		// Standalone markers without a length payload: RSTn (FF D0–D7) and TEM
		// (FF 01). Copy the 2-byte marker and continue.
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			buf.Write(data[i : i+2])
			i += 2
			continue
		}

		// All other markers carry a 2-byte big-endian length (which includes the
		// two length bytes themselves) followed by the segment payload.
		if i+3 >= len(data) {
			return nil, false, false
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 {
			return nil, false, false
		}
		segStart := i + 2           // first byte of the length field
		segEnd := segStart + segLen // one past the end of the segment payload
		if segEnd > len(data) {
			// Truncated/declared-too-long segment: refuse to rewrite.
			return nil, false, false
		}

		// Drop APP1 segments whose payload is an Exif block; copy everything else.
		if marker == markerAPP1 && isExifAPP1(data[segStart:segEnd]) {
			removed = true
		} else {
			buf.Write(data[i:segEnd]) // marker (FF Ex) + length + payload
		}
		i = segEnd
	}

	// Reached the end without an SOS/EOI. Only trust the result if we actually
	// removed something and the buffer is non-trivial; otherwise no-op.
	if !removed {
		return nil, false, true
	}
	return buf.Bytes(), removed, true
}

// isExifAPP1 reports whether an APP1 segment body (starting at its 2-byte length
// field) carries the "Exif\0\0" signature.
func isExifAPP1(seg []byte) bool {
	// seg = [lenHi lenLo payload...]; the Exif signature starts at the payload.
	if len(seg) < 2+len(exifSignature) {
		return false
	}
	return bytes.Equal(seg[2:2+len(exifSignature)], exifSignature)
}

// dirOf returns the directory component of a path, defaulting to "." so
// os.CreateTemp lands beside the original file (enabling a same-filesystem
// rename).
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
