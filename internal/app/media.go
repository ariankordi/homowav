// This file puts a file somewhere public and returns its URL, so a
// message can point at it.
//
// MediaStore is the seam for providers. S3 is the one implementation: it speaks
// the S3 API, which Amazon S3, Cloudflare R2, Backblaze B2, Wasabi and MinIO
// all accept, using only the standard library.

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	// keyRandomBytes is the random tail of an object name. The date and file
	// name make keys guessable on a public bucket; this keeps them from being
	// enumerated (64 bits is out of reach even when the second and the
	// slug are known), and two uploads in the same second from colliding.
	keyRandomBytes = 8
	// maxExtensionLength keeps a hostile file name from becoming a long key.
	maxExtensionLength = 8
	// maxSlugLength caps the part of a key taken from the file name. The name
	// only has to be recognizable, not complete.
	maxSlugLength = 32
	// keyTimeLayout is the date and time at the start of a key, in local time,
	// so keys sort by upload time.
	keyTimeLayout = "2006-01-02_150405"
)

var (
	// youTubeIDPattern finds the "[id]" that yt-dlp puts at the end of its
	// default file names ("%(title)s [%(id)s].%(ext)s"). The id is case
	// sensitive, so it is kept as is and never cut off by maxSlugLength.
	youTubeIDPattern = regexp.MustCompile(`\[([A-Za-z0-9_-]{11})\]`)
	// cameraNamePattern matches names cameras and phones make up, such as
	// IMG_1234 or PXL_20261102_143005123. They say nothing the date does not.
	cameraNamePattern = regexp.MustCompile(`(?i)^(img|vid|mov|dsc[fn]?|pxl|mvimg|image|video)([ _-]?[0-9][0-9_-]*)?$`)
	// unsafeRunPattern is a run of anything but letters, digits and "_". Hyphens
	// are in it too, so " - " becomes one "-" instead of three.
	unsafeRunPattern = regexp.MustCompile(`[^A-Za-z0-9_]+`)
)

// MediaStore uploads one object and returns the URL a recipient can fetch it from.
type MediaStore interface {
	// Put stores exactly size bytes read from body under key, with the given
	// content type. The body is streamed, never held in memory as a whole.
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) (publicURL string, err error)
}

// NewMediaKey builds an object key such as
// "hw/2026-11-02_143005_Never-Gonna-Give-You-Up_dQw4w9WgXcQ_9f2c4e1a7b3d5068.mp4":
// prefix, the upload time, a short slug of fileName, a random tail, and the
// extension if it is a plain alphanumeric one. The slug keeps letter case.
func NewMediaKey(prefix, fileName string, now time.Time) (string, error) {
	raw := make([]byte, keyRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	parts := []string{now.Format(keyTimeLayout)}
	if slug := mediaSlug(fileName); slug != "" {
		parts = append(parts, slug)
	}
	parts = append(parts, hex.EncodeToString(raw))
	return prefix + strings.Join(parts, "_") + safeExtension(fileName), nil
}

// mediaSlug shortens a file name to letters, digits, "-" and "_", keeping a
// yt-dlp video id whole. It returns "" for camera-made names and for names
// with nothing usable (for example, only non-Latin letters).
func mediaSlug(fileName string) string {
	// Browsers send a bare name, but a Windows path must not leak its folders.
	base := path.Base(strings.ReplaceAll(fileName, "\\", "/"))
	stem := strings.TrimSuffix(base, path.Ext(base))
	videoID := ""
	if match := youTubeIDPattern.FindStringSubmatchIndex(stem); match != nil {
		videoID = stem[match[2]:match[3]]
		stem = stem[:match[0]] + stem[match[1]:]
	}
	title := strings.Trim(unsafeRunPattern.ReplaceAllString(stem, "-"), "-_")
	if cameraNamePattern.MatchString(title) {
		title = ""
	}
	budget := maxSlugLength
	if videoID != "" {
		budget -= len(videoID) + 1 // The id and the "_" before it.
	}
	title = cutAtWord(title, budget)
	switch {
	case title == "":
		return videoID
	case videoID == "":
		return title
	default:
		return title + "_" + videoID
	}
}

// cutAtWord shortens an ASCII slug to at most limit bytes, at the last "-" or
// "_" when there is one, so words are not cut in half.
func cutAtWord(slug string, limit int) string {
	if len(slug) <= limit {
		return slug
	}
	cut := slug[:limit]
	if boundary := strings.LastIndexAny(cut, "-_"); boundary > 0 {
		cut = cut[:boundary]
	}
	return strings.TrimRight(cut, "-_")
}

// safeExtension returns a lower-case ".ext" or "" if the extension has
// anything but letters and digits.
func safeExtension(fileName string) string {
	extension := strings.ToLower(path.Ext(fileName))
	if len(extension) < 2 || len(extension) > maxExtensionLength+1 {
		return ""
	}
	for _, char := range extension[1:] {
		isLetter := char >= 'a' && char <= 'z'
		isDigit := char >= '0' && char <= '9'
		if !isLetter && !isDigit {
			return ""
		}
	}
	return extension
}
