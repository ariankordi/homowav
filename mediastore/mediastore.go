// Package mediastore puts a file somewhere public and returns its URL, so a
// message can point at it.
//
// Store is the seam for providers. S3 is the one implementation: it speaks
// the S3 API, which Amazon S3, Cloudflare R2, Backblaze B2, Wasabi and MinIO
// all accept, using only the standard library.
package mediastore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path"
	"strings"
)

const (
	// keyRandomBytes makes object names unguessable and collision-free.
	keyRandomBytes = 12
	// maxExtensionLength keeps a hostile file name from becoming a long key.
	maxExtensionLength = 8
)

// Store uploads one object and returns the URL a recipient can fetch it from.
type Store interface {
	// Put stores data under key with the given content type.
	Put(ctx context.Context, key, contentType string, data []byte) (publicURL string, err error)
}

// NewKey builds an object key: prefix, a random name, and the extension of
// fileName if it is a plain alphanumeric one. The original name is dropped on
// purpose, so nothing personal ends up in a public URL.
func NewKey(prefix, fileName string) (string, error) {
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
