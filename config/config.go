// Package config holds the settings a user changes from the UI, persisted as
// one JSON file in the data directory.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"hwclient/fileutil"
)

// pairingSecretBytes is the size of a generated pairing secret (128 bits).
const pairingSecretBytes = 16

// Media describes an S3-compatible bucket that uploaded media is stored in.
// The zero value means no bucket is configured.
type Media struct {
	// Endpoint is the service URL for non-AWS providers (R2, B2, MinIO).
	// Empty means AWS.
	Endpoint string `json:"endpoint"`
	// Region is the signing region, for example "us-east-1".
	Region string `json:"region"`
	// Bucket is the bucket name.
	Bucket string `json:"bucket"`
	// AccessKeyID and SecretAccessKey sign the upload.
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	// PublicBaseURL is what a recipient fetches objects from. Empty derives
	// it from the bucket.
	PublicBaseURL string `json:"public_base_url"`
	// KeyPrefix is prepended to every object key, for example "hw/".
	KeyPrefix string `json:"key_prefix"`
	// PathStyle addresses the bucket as endpoint/bucket/key instead of
	// bucket.endpoint/key. MinIO and some providers need it.
	PathStyle bool `json:"path_style"`
	// ACL is sent as x-amz-acl, for example "public-read". Empty sends none,
	// for buckets made public by policy.
	ACL string `json:"acl"`
}

// Configured reports whether enough is set to attempt an upload.
func (m Media) Configured() bool {
	return m.Bucket != "" && m.AccessKeyID != "" && m.SecretAccessKey != "" && m.Region != ""
}

// Config is the whole settings file.
type Config struct {
	// PairingSecret admits other devices on the LAN (see package access).
	PairingSecret string `json:"pairing_secret"`
	// VisitorID overrides the id captured from the app's traffic.
	VisitorID string `json:"visitor_id"`
	// Media is the upload bucket.
	Media Media `json:"media"`
}

// Store holds the current Config and persists changes. It is safe for
// concurrent use.
type Store struct {
	mu     sync.RWMutex
	config Config
	path   string
}

// Load reads path, or starts a new file with a random pairing secret if it
// does not exist. An empty path keeps the settings in memory only.
func Load(path string) (*Store, error) {
	store := &Store{path: path}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("config: %w", err)
		default:
			if err := json.Unmarshal(data, &store.config); err != nil {
				return nil, fmt.Errorf("config: parsing %s: %w", path, err)
			}
		}
	}
	if store.config.PairingSecret == "" {
		secret, err := NewSecret()
		if err != nil {
			return nil, err
		}
		store.config.PairingSecret = secret
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// Get returns a copy of the current settings.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// Update applies edit to a copy of the settings and persists the result.
func (s *Store) Update(edit func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	edit(&next)
	previous := s.config
	s.config = next
	if err := s.saveLocked(); err != nil {
		s.config = previous
		return err
	}
	return nil
}

// saveLocked writes the file with a private mode: it holds the bucket keys.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.config, "", "\t")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(s.path, append(data, '\n'), fileutil.PrivateFileMode)
}

// NewSecret returns a random hex pairing secret.
func NewSecret() (string, error) {
	raw := make([]byte, pairingSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("config: generating secret: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
