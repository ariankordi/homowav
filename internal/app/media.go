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
	"strings"
)

const (
	// keyRandomBytes makes object names unguessable and collision-free.
	keyRandomBytes = 12
	// maxExtensionLength keeps a hostile file name from becoming a long key.
	maxExtensionLength = 8
)

// MediaStore uploads one object and returns the URL a recipient can fetch it from.
type MediaStore interface {
	// Put stores exactly size bytes read from body under key, with the given
	// content type. The body is streamed, never held in memory as a whole.
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) (publicURL string, err error)
}

// NewMediaKey builds an object key: prefix, a random name, and the extension of
// fileName if it is a plain alphanumeric one. The original name is dropped on
// purpose, so nothing personal ends up in a public URL.
func NewMediaKey(prefix, fileName string) (string, error) {
	raw := make([]byte, keyRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw) + safeExtension(fileName), nil
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
