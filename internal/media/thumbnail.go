package media

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HasFFmpeg reports whether the ffmpeg CLI is available on PATH. All ffmpeg-
// dependent features (webp/jxl compression, video thumbnails) check this and
// degrade gracefully when it returns false.
func HasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// VideoThumbnail extracts a single representative frame from the video at
// srcPath and writes it to outPath using ffmpeg. The output format is determined
// by outPath's extension (e.g. .jpg, .png, .webp), which the caller chooses.
//
// It runs:
//
//	ffmpeg -y -i <src> -vf thumbnail -frames:v 1 <outPath>
//
// Return semantics:
//   - (true, nil)  a thumbnail frame was produced.
//   - (false, nil) the input has no usable video stream (e.g. an audio-only
//     file), the output file was not produced, or ffmpeg is unavailable. These
//     are expected, non-fatal outcomes.
//   - (false, err) ffmpeg failed for some other reason.
//
// The format argument is informational; the actual container/codec is implied by
// outPath's extension. It is accepted so callers can keep their intent explicit.
func VideoThumbnail(srcPath, outPath, format string) (ok bool, err error) {
	_ = format // extension on outPath drives the encoder; kept for caller clarity.

	if !HasFFmpeg() {
		// No ffmpeg: thumbnailing simply isn't available. Treat as "not produced".
		return false, nil
	}

	// -vf thumbnail asks ffmpeg to pick the most representative frame; -frames:v 1
	// limits output to that single frame.
	args := []string{
		"-y",
		"-i", srcPath,
		"-vf", "thumbnail",
		"-frames:v", "1",
		outPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Audio-only inputs (no video stream) make ffmpeg complain that the output
	// "does not contain any stream". That's an expected, non-fatal case.
	if runErr != nil {
		if strings.Contains(stderr.String(), "does not contain any stream") {
			cleanupEmpty(outPath)
			return false, nil
		}
		return false, fmt.Errorf("media: ffmpeg thumbnail: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}

	// Even on success, confirm a non-empty output file actually exists.
	if !fileHasContent(outPath) {
		cleanupEmpty(outPath)
		return false, nil
	}
	return true, nil
}

// ThumbnailMime maps a thumbnail format/extension to its MIME type, defaulting to
// image/jpeg for unknown formats.
func ThumbnailMime(format string) string {
	switch normalizeType(format) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// fileHasContent reports whether path exists and is a non-empty regular file.
func fileHasContent(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fi.IsDir() && fi.Size() > 0
}

// cleanupEmpty removes an empty/partial output file ffmpeg may have created, so
// we never leave a zero-byte thumbnail behind. Errors are ignored intentionally.
func cleanupEmpty(path string) {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() && fi.Size() == 0 {
		_ = os.Remove(path)
	}
}
