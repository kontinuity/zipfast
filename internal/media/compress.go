// Package media implements image compression, video thumbnail generation, and
// best-effort EXIF GPS stripping for uploaded files.
//
// Design goal: LOW MEMORY. Formats the Go standard library can handle natively
// (jpeg/png) are processed in-process, but anything heavier (webp/jxl encoding,
// video decoding) is delegated to the ffmpeg CLI, which runs as a separate
// process. We never link native image/codec libraries via cgo.
//
// All ffmpeg-dependent features degrade gracefully: if ffmpeg is not present on
// the system, webp/jxl compression falls back to JPEG and video thumbnailing is
// simply skipped (reported as "not produced").
package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"strings"

	// Register decoders so image.Decode can sniff JPEG, PNG, and GIF inputs.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// CompressTypes lists the output formats the compressor understands. jpg/jpeg
// and png are always available via the standard library; webp and jxl require
// ffmpeg (see Supported).
var CompressTypes = []string{"jpg", "jpeg", "png", "webp", "jxl"}

// defaultJPEGQuality is used when a caller passes a quality outside the valid
// JPEG range (1-100).
const defaultJPEGQuality = 75

// Supported reports whether the compressor can produce the given output type on
// this system. jpg/jpeg/png are always supported (standard library); webp/jxl
// are supported only when the ffmpeg CLI is available.
func Supported(typ string) bool {
	switch normalizeType(typ) {
	case "jpg", "jpeg", "png":
		return true
	case "webp", "jxl":
		return HasFFmpeg()
	default:
		return false
	}
}

// Compress reads the image at srcPath and re-encodes it as typ at the requested
// quality. It returns the encoded bytes together with the MIME type and the
// canonical file extension to use for the result.
//
//   - jpg/jpeg: decoded in-process and re-encoded as JPEG.
//   - png:      decoded in-process and re-encoded as PNG (lossless; quality is
//     ignored by the PNG encoder).
//   - webp/jxl: transcoded with ffmpeg when available; otherwise (ffmpeg absent,
//     or this ffmpeg build lacks the requested encoder) the input is re-encoded
//     as JPEG as a graceful fallback so callers still get a compressed result.
//
// Any failure of the stdlib path (jpeg/png decode or encode) is returned as an
// error; callers treat that as "compression failed" and typically fall back to
// storing the original upload.
func Compress(srcPath, typ string, quality int) (data []byte, mime string, ext string, err error) {
	typ = normalizeType(typ)
	q := clampQuality(quality)

	switch typ {
	case "jpg", "jpeg":
		return compressJPEG(srcPath, q)
	case "png":
		return compressPNG(srcPath)
	case "webp", "jxl":
		// Prefer ffmpeg for these formats. Fall back to JPEG when ffmpeg is
		// absent, and also when the transcode itself fails (e.g. this ffmpeg
		// build wasn't compiled with the webp/jxl encoder) so the upload still
		// succeeds with a compressed result rather than erroring out.
		if HasFFmpeg() {
			if data, mime, ext, err = compressFFmpeg(srcPath, typ, q); err == nil {
				return data, mime, ext, nil
			}
		}
		return compressJPEG(srcPath, q)
	default:
		return nil, "", "", fmt.Errorf("media: unsupported compress type %q", typ)
	}
}

// compressJPEG decodes any supported input image and re-encodes it as JPEG.
func compressJPEG(srcPath string, quality int) ([]byte, string, string, error) {
	img, err := decodeImage(srcPath)
	if err != nil {
		return nil, "", "", err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, "", "", fmt.Errorf("media: jpeg encode: %w", err)
	}
	return buf.Bytes(), "image/jpeg", "jpg", nil
}

// compressPNG decodes any supported input image and re-encodes it as PNG.
func compressPNG(srcPath string) ([]byte, string, string, error) {
	img, err := decodeImage(srcPath)
	if err != nil {
		return nil, "", "", err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, "", "", fmt.Errorf("media: png encode: %w", err)
	}
	return buf.Bytes(), "image/png", "png", nil
}

// compressFFmpeg transcodes srcPath to webp or jxl using the ffmpeg CLI. ffmpeg
// writes to a temporary file (chosen by extension) which we then read back into
// memory. The caller is expected to have checked HasFFmpeg first.
func compressFFmpeg(srcPath, typ string, quality int) ([]byte, string, string, error) {
	tmp, err := os.CreateTemp("", "zipfast-compress-*."+typ)
	if err != nil {
		return nil, "", "", fmt.Errorf("media: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	// -y overwrites the (already-created) temp file. -qscale:v maps our 1-100
	// quality onto ffmpeg's per-codec quality scale; libwebp accepts 0-100
	// directly, and for jxl higher is better quality as well.
	args := []string{
		"-y",
		"-i", srcPath,
		"-qscale:v", fmt.Sprintf("%d", quality),
		tmpPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", "", fmt.Errorf("media: ffmpeg transcode to %s: %w: %s", typ, err, strings.TrimSpace(stderr.String()))
	}

	out, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("media: read transcoded file: %w", err)
	}
	if len(out) == 0 {
		return nil, "", "", fmt.Errorf("media: ffmpeg produced empty %s output", typ)
	}

	switch typ {
	case "webp":
		return out, "image/webp", "webp", nil
	default: // jxl
		return out, "image/jxl", "jxl", nil
	}
}

// decodeImage opens srcPath and decodes it using the registered standard-library
// decoders (jpeg/png/gif).
func decodeImage(srcPath string) (image.Image, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("media: open source: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("media: decode source: %w", err)
	}
	return img, nil
}

// normalizeType lowercases and strips a leading dot from a type/extension so
// callers may pass "JPG", ".jpg", or "jpg" interchangeably.
func normalizeType(typ string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typ)), ".")
}

// clampQuality keeps the JPEG/ffmpeg quality within the valid 1-100 range,
// substituting a sane default for out-of-range values.
func clampQuality(q int) int {
	if q < 1 || q > 100 {
		return defaultJPEGQuality
	}
	return q
}
