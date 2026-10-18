// This file creates and maintains the private CA and the leaf certificate
// the proxy presents for the intercepted host.
//
// It does in Go what scripts/make-cert.sh does with openssl, with the same
// parameters, so the program can set itself up without any tools installed.

package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"hwclient/internal/session"
)

const (
	// File names inside the certificate directory.
	caCertFile   = "ca.pem"
	caKeyFile    = "ca.key"
	leafCertFile = "leaf.pem"
	leafKeyFile  = "leaf.key"

	// caCommonName is what the phone shows for the profile.
	caCommonName = Name + " private CA"
	// caLifetime is long because replacing the CA means re-trusting it on the phone.
	caLifetime = 10 * 365 * 24 * time.Hour
	// leafLifetime stays under the 398 days Apple platforms accept for a server certificate.
	leafLifetime = 397 * 24 * time.Hour
	// renewBefore re-issues the leaf this long before it expires.
	renewBefore = 30 * 24 * time.Hour
	// backdate absorbs clock differences between this machine and the phone.
	backdate = time.Hour
	// serialBits is the size of random certificate serial numbers.
	serialBits = 127
	// publicFileMode is for certificates, which are not secret.
	publicFileMode fs.FileMode = 0o644
)

// CertManager owns the files in one directory and serves the current leaf.
// It is safe for concurrent use.
type CertManager struct {
	dir   string
	hosts []string
	now   func() time.Time

	mu   sync.Mutex
	leaf *tls.Certificate
}

// NewCertManager prepares dir (creating the CA and leaf if missing) for hosts.
func NewCertManager(dir string, hosts []string) (*CertManager, error) {
	if len(hosts) == 0 {
		return nil, errors.New("certs: at least one host name is required")
	}
	if err := os.MkdirAll(dir, session.PrivateDirMode); err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}
	manager := &CertManager{dir: dir, hosts: slices.Clone(hosts), now: time.Now}
	if _, err := manager.current(); err != nil {
		return nil, err
	}
	return manager, nil
}

// GetCertificate is a tls.Config.GetCertificate callback. It re-issues the
// leaf shortly before expiry, so a process that runs for years keeps working.
func (m *CertManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.current()
}

// CAPath is the PEM file to install on the phone.
func (m *CertManager) CAPath() string { return filepath.Join(m.dir, caCertFile) }

// CAPEM returns the CA certificate in PEM form. It is public information.
func (m *CertManager) CAPEM() ([]byte, error) { return os.ReadFile(m.CAPath()) }

// CAFingerprint returns the SHA-256 of the CA certificate as hex, the value
// iOS shows in the profile details, so a user can compare the two.
func (m *CertManager) CAFingerprint() (string, error) {
	cert, _, err := m.loadCA()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// current returns a valid leaf, loading or issuing one as needed.
func (m *CertManager) current() (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leaf != nil && m.usable(m.leaf.Leaf) {
		return m.leaf, nil
	}
	if loaded, err := tls.LoadX509KeyPair(filepath.Join(m.dir, leafCertFile), filepath.Join(m.dir, leafKeyFile)); err == nil {
		if parsed, err := x509.ParseCertificate(loaded.Certificate[0]); err == nil && m.usable(parsed) {
			loaded.Leaf = parsed
			m.leaf = &loaded
			return m.leaf, nil
		}
	}
	issued, err := m.issueLeaf()
	if err != nil {
		return nil, err
	}
	m.leaf = issued
	return m.leaf, nil
}

// usable reports a leaf that covers exactly our hosts and is not about to expire.
func (m *CertManager) usable(leaf *x509.Certificate) bool {
	if leaf == nil || m.now().Add(renewBefore).After(leaf.NotAfter) {
		return false
	}
	want, got := slices.Clone(m.hosts), slices.Clone(leaf.DNSNames)
	slices.Sort(want)
	slices.Sort(got)
	return slices.Equal(want, got)
}

// loadCA reads the CA, creating it first if the directory has none. An
// existing CA is always kept: re-issuing only the leaf needs no change on the phone.
func (m *CertManager) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, certErr := os.ReadFile(filepath.Join(m.dir, caCertFile))
	keyPEM, keyErr := os.ReadFile(filepath.Join(m.dir, caKeyFile))
	if errors.Is(certErr, fs.ErrNotExist) && errors.Is(keyErr, fs.ErrNotExist) {
		return m.createCA()
	}
	if err := errors.Join(certErr, keyErr); err != nil {
		return nil, nil, fmt.Errorf("certs: reading CA: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, nil, errors.New("certs: CA files are not PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: parsing CA: %w", err)
	}
	key, err := parseECKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: parsing CA key: %w", err)
	}
	return cert, key, nil
}

// parseECKey accepts both encodings openssl has produced over the years, so a
// CA made by scripts/make-cert.sh keeps working.
func parseECKey(der []byte) (*ecdsa.PrivateKey, error) {
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return key, nil
}

func (m *CertManager) createCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := m.now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: caCommonName},
		NotBefore:    now.Add(-backdate),
		NotAfter:     now.Add(caLifetime),
		// iOS refuses an issuer without basicConstraints CA:TRUE.
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: creating CA: %w", err)
	}
	if err := m.writePair(caCertFile, caKeyFile, der, key); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func (m *CertManager) issueLeaf() (*tls.Certificate, error) {
	caCert, caKey, err := m.loadCA()
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := m.now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: m.hosts[0]},
		// Apple platforms require both a SAN and the serverAuth usage.
		DNSNames:              m.hosts,
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(leafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("certs: issuing leaf: %w", err)
	}
	if err := m.writePair(leafCertFile, leafKeyFile, der, key); err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// writePair stores a certificate and its key as PEM files.
func (m *CertManager) writePair(certFile, keyFile string, certDER []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := session.WriteAtomic(filepath.Join(m.dir, keyFile), keyPEM, session.PrivateFileMode); err != nil {
		return err
	}
	return session.WriteAtomic(filepath.Join(m.dir, certFile), certPEM, publicFileMode)
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
}
