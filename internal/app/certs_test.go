package app

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hwclient/internal/session"
)

const testHost = "api.example.com"

func TestManagerIssuesAVerifiableLeafAndKeepsTheCA(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewCertManager(dir, []string{testHost})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := manager.CAPEM()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("CA PEM did not parse")
	}
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{DNSName: testHost, Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("leaf does not verify against the CA: %v", err)
	}
	if lifetime := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore); lifetime > 398*24*time.Hour {
		t.Errorf("leaf lifetime %v exceeds what Apple platforms accept", lifetime)
	}
	for _, name := range []string{caKeyFile, leafKeyFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != session.PrivateFileMode {
			t.Errorf("%s: mode %v, err %v; want a private file", name, info.Mode().Perm(), err)
		}
	}
	fingerprint, err := manager.CAFingerprint()
	if err != nil || len(fingerprint) != 64 {
		t.Errorf("fingerprint = %q, %v", fingerprint, err)
	}

	// A second manager on the same directory reuses both files.
	again, err := NewCertManager(dir, []string{testHost})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _ := again.GetCertificate(nil)
	if !reloaded.Leaf.Equal(leaf.Leaf) {
		t.Error("an unexpired leaf was re-issued on restart")
	}
	if againFingerprint, _ := again.CAFingerprint(); againFingerprint != fingerprint {
		t.Error("the CA changed on restart: the phone would have to trust a new one")
	}
}

func TestManagerReissuesLeafNearExpiryAndOnHostChange(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewCertManager(dir, []string{testHost})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := manager.GetCertificate(nil)
	fingerprint, _ := manager.CAFingerprint()

	// Move the clock to just inside the renewal window.
	manager.now = func() time.Time { return first.Leaf.NotAfter.Add(-renewBefore + time.Minute) }
	second, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Leaf.Equal(first.Leaf) || !second.Leaf.NotAfter.After(first.Leaf.NotAfter) {
		t.Error("leaf was not re-issued inside the renewal window")
	}

	other, err := NewCertManager(dir, []string{"other.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	third, _ := other.GetCertificate(nil)
	if len(third.Leaf.DNSNames) != 1 || third.Leaf.DNSNames[0] != "other.example.com" {
		t.Errorf("leaf names after host change = %v", third.Leaf.DNSNames)
	}
	if otherFingerprint, _ := other.CAFingerprint(); otherFingerprint != fingerprint {
		t.Error("the CA changed when only the leaf needed re-issuing")
	}
}
