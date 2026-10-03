// Package autocert creates a per-machine certificate authority and a
// certificate for 127.0.0.1, and records that authority in the OS trust store.
package autocert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	// CAName is the authority certificate stored in the data directory.
	CAName = "ca.pem"
	// CAKeyName is the authority private key. It stays on this machine.
	CAKeyName = "ca-key.pem"
	// LeafName is the server certificate served on the local listener.
	LeafName = "cert.pem"
	// LeafKeyName is the server private key.
	LeafKeyName = "key.pem"

	caCommonName = "HE Gateway Local CA"
	orgName      = "HE Gateway"

	renewWithin  = 30 * 24 * time.Hour
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 825 * 24 * time.Hour
)

// Installer records the local authority in the operating system trust store.
// Installed must not prompt. Install may show the system consent dialog.
type Installer interface {
	Installed(ctx context.Context, caPEM []byte) (bool, error)
	Install(ctx context.Context, caPath string) error
}

// DataDir is the per-user directory that holds the authority and the leaf.
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "windows", "darwin":
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("user config directory: %w", err)
		}
		return filepath.Join(base, "HE Gateway"), nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "he-gateway"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", "he-gateway"), nil
	}
}

// Ensure writes a certificate authority and a leaf for localhost into dir.
// An existing leaf is replaced when it expires within 30 days. The authority
// is kept until it is itself inside that window, so renewal does not require
// a new trust prompt.
func Ensure(dir string, now time.Time) (certPath, keyPath string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create certificate directory: %w", err)
	}
	caCert, caKey, created, err := loadOrCreateCA(dir, now)
	if err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, LeafName)
	keyPath = filepath.Join(dir, LeafKeyName)
	if !created && leafUsable(certPath, now) {
		return certPath, keyPath, nil
	}
	if err := writeLeaf(dir, caCert, caKey, now); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// Trust installs the authority when the store does not already have it.
func Trust(ctx context.Context, caPath string, inst Installer) error {
	pemBytes, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read local ca: %w", err)
	}
	ok, err := inst.Installed(ctx, pemBytes)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := inst.Install(ctx, caPath); err != nil {
		return err
	}
	return nil
}

// CheckInstallTarget reports whether path is a HE Gateway authority certificate
// named ca.pem. The elevated helper uses this so it will not install an
// arbitrary file after the user accepts the system prompt.
func CheckInstallTarget(caPath string) error {
	if filepath.Base(caPath) != CAName {
		return fmt.Errorf("refusing %s", filepath.Base(caPath))
	}
	cert, err := readCert(caPath)
	if err != nil {
		return err
	}
	if !cert.IsCA || cert.Subject.CommonName != caCommonName || !hasOrg(cert, orgName) {
		return fmt.Errorf("refusing certificate that is not the local gateway authority")
	}
	return nil
}

// InstallElevated installs a checked authority into the machine trust store.
// The caller is already elevated (UAC on Windows, pkexec on Linux).
func InstallElevated(caPath string) error {
	if err := CheckInstallTarget(caPath); err != nil {
		return err
	}
	if err := installMachine(caPath); err != nil {
		return fmt.Errorf("install local ca: %w", err)
	}
	return nil
}

func loadOrCreateCA(dir string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, bool, error) {
	cert, key, err := loadCA(dir)
	if err == nil && validUntil(cert, now) {
		return cert, key, false, nil
	}
	cert, key, err = createCA(dir, now)
	if err != nil {
		return nil, nil, false, err
	}
	return cert, key, true, nil
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := readCert(filepath.Join(dir, CAName))
	if err != nil {
		return nil, nil, err
	}
	key, err := readKey(filepath.Join(dir, CAKeyName))
	if err != nil {
		return nil, nil, err
	}
	if !cert.IsCA || !samePublicKey(cert, key) {
		return nil, nil, fmt.Errorf("stored authority does not match its key")
	}
	return cert, key, nil
}

func createCA(dir string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName, Organization: []string{orgName}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("generate certificate authority: %w", err)
	}
	keyDER, err := marshalECKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(filepath.Join(dir, CAKeyName), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(filepath.Join(dir, CAName), "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate authority: %w", err)
	}
	return cert, key, nil
}

func writeLeaf(dir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) error {
	key, err := newKey()
	if err != nil {
		return err
	}
	serial, err := newSerial()
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("generate server certificate: %w", err)
	}
	keyDER, err := marshalECKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(filepath.Join(dir, LeafKeyName), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	if err := writePEM(filepath.Join(dir, LeafName), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return nil
}

func leafUsable(path string, now time.Time) bool {
	cert, err := readCert(path)
	if err != nil {
		return false
	}
	if !validUntil(cert, now) || !hasDNS(cert, "localhost") || !hasIP(cert, net.ParseIP("127.0.0.1")) {
		return false
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}

func validUntil(cert *x509.Certificate, now time.Time) bool {
	if now.Before(cert.NotBefore) {
		return false
	}
	return cert.NotAfter.After(now.Add(renewWithin))
}

func hasDNS(cert *x509.Certificate, name string) bool {
	for _, dns := range cert.DNSNames {
		if dns == name {
			return true
		}
	}
	return false
}

func hasIP(cert *x509.Certificate, ip net.IP) bool {
	for _, candidate := range cert.IPAddresses {
		if candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func hasOrg(cert *x509.Certificate, org string) bool {
	for _, candidate := range cert.Subject.Organization {
		if candidate == org {
			return true
		}
	}
	return false
}

func samePublicKey(cert *x509.Certificate, key *ecdsa.PrivateKey) bool {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	return pub.Equal(&key.PublicKey)
}

func newKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

func newSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

func marshalECKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode key: %w", err)
	}
	return der, nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	if len(der) == 0 {
		return fmt.Errorf("encode %s", path)
	}
	body := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readCert(path string) (*x509.Certificate, error) {
	der, err := readPEM(path, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s: %w", path, err)
	}
	return cert, nil
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	der, err := readPEM(path, "EC PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	return key, nil
}

func readPEM(path, blockType string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != blockType {
		return nil, fmt.Errorf("parse %s", path)
	}
	return block.Bytes, nil
}
