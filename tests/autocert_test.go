package tests

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"he-gateway/internal/autocert"
)

func TestEnsureLeafAndAuthority(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	certPath, keyPath, err := autocert.Ensure(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if certPath != filepath.Join(dir, autocert.LeafName) || keyPath != filepath.Join(dir, autocert.LeafKeyName) {
		t.Fatalf("paths %s %s", certPath, keyPath)
	}

	leaf := parseCertFile(t, certPath)
	if got := leaf.DNSNames; len(got) != 1 || got[0] != "localhost" {
		t.Fatalf("dns = %#v", got)
	}
	if !hasIP(leaf, net.ParseIP("127.0.0.1")) {
		t.Fatalf("ips = %v", leaf.IPAddresses)
	}
	if !hasExtUsage(leaf, x509.ExtKeyUsageServerAuth) {
		t.Fatalf("ext key usage = %v", leaf.ExtKeyUsage)
	}

	ca := parseCertFile(t, filepath.Join(dir, autocert.CAName))
	if !ca.IsCA {
		t.Fatal("authority is not a CA")
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("key usage = %v", ca.KeyUsage)
	}
	if ca.Subject.CommonName != "HE Gateway Local CA" {
		t.Fatalf("cn = %s", ca.Subject.CommonName)
	}

	info, err := os.Stat(filepath.Join(dir, autocert.CAKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("ca key mode %o", info.Mode().Perm())
	}
}

func TestEnsureRenewsLeafAndKeepsAuthority(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	if _, _, err := autocert.Ensure(dir, now); err != nil {
		t.Fatal(err)
	}
	firstLeaf := parseCertFile(t, filepath.Join(dir, autocert.LeafName))
	firstCA := parseCertFile(t, filepath.Join(dir, autocert.CAName))

	if _, _, err := autocert.Ensure(dir, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	sameLeaf := parseCertFile(t, filepath.Join(dir, autocert.LeafName))
	if firstLeaf.SerialNumber.Cmp(sameLeaf.SerialNumber) != 0 {
		t.Fatal("leaf was renewed while it was still valid")
	}

	later := firstLeaf.NotAfter.Add(-29 * 24 * time.Hour)
	if _, _, err := autocert.Ensure(dir, later); err != nil {
		t.Fatal(err)
	}
	renewed := parseCertFile(t, filepath.Join(dir, autocert.LeafName))
	if firstLeaf.SerialNumber.Cmp(renewed.SerialNumber) == 0 {
		t.Fatal("expected a renewed leaf")
	}
	keptCA := parseCertFile(t, filepath.Join(dir, autocert.CAName))
	if firstCA.SerialNumber.Cmp(keptCA.SerialNumber) != 0 {
		t.Fatal("authority changed during leaf renewal")
	}
	if err := autocert.CheckInstallTarget(filepath.Join(dir, autocert.CAName)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckInstallTargetRejectsOtherFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := autocert.Ensure(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := autocert.CheckInstallTarget(filepath.Join(dir, autocert.LeafName)); err == nil {
		t.Fatal("expected the leaf path to be refused")
	}
	body, err := os.ReadFile(filepath.Join(dir, autocert.LeafName))
	if err != nil {
		t.Fatal(err)
	}
	disguised := filepath.Join(t.TempDir(), autocert.CAName)
	if err := os.WriteFile(disguised, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := autocert.CheckInstallTarget(disguised); err == nil {
		t.Fatal("expected a non-authority certificate to be refused")
	}
}

func TestTrustSkipsInstalledCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caPath := filepath.Join(dir, autocert.CAName)
	if err := os.WriteFile(caPath, []byte("pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := &fakeInstaller{installed: true}
	if err := autocert.Trust(context.Background(), caPath, installed); err != nil {
		t.Fatal(err)
	}
	if installed.calls != 0 {
		t.Fatalf("install calls = %d", installed.calls)
	}

	missing := &fakeInstaller{}
	if err := autocert.Trust(context.Background(), caPath, missing); err != nil {
		t.Fatal(err)
	}
	if missing.calls != 1 {
		t.Fatalf("install calls = %d", missing.calls)
	}
}

func TestDataDir(t *testing.T) {
	t.Parallel()
	dir, err := autocert.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("data dir %s", dir)
	}
}

type fakeInstaller struct {
	installed bool
	calls     int
}

func (f *fakeInstaller) Installed(context.Context, []byte) (bool, error) {
	return f.installed, nil
}

func (f *fakeInstaller) Install(context.Context, string) error {
	f.calls++
	return nil
}

func parseCertFile(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(body)
	if block == nil {
		t.Fatalf("no pem in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func hasIP(cert *x509.Certificate, ip net.IP) bool {
	for _, candidate := range cert.IPAddresses {
		if candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func hasExtUsage(cert *x509.Certificate, usage x509.ExtKeyUsage) bool {
	for _, candidate := range cert.ExtKeyUsage {
		if candidate == usage {
			return true
		}
	}
	return false
}
