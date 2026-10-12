package config

import (
	"os"
	"path/filepath"
	"testing"

	"hwclient/fileutil"
)

func TestLoadCreatesSecretAndUpdatePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	secret := store.Get().PairingSecret
	if len(secret) != pairingSecretBytes*2 {
		t.Fatalf("generated secret %q has the wrong length", secret)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != fileutil.PrivateFileMode {
		t.Fatalf("config file: mode %v, err %v", info.Mode().Perm(), err)
	}
	if store.Get().Media.Configured() {
		t.Error("an empty media section counts as configured")
	}

	err = store.Update(func(c *Config) {
		c.VisitorID = "42"
		c.Media = Media{Region: "us-east-1", Bucket: "b", AccessKeyID: "id", SecretAccessKey: "key"}
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Get()
	if got.PairingSecret != secret || got.VisitorID != "42" || !got.Media.Configured() {
		t.Errorf("reloaded config = %+v", got)
	}
}
