package pki

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllHistoricalCAFingerprintsAreRejected(t *testing.T) {
	for i, fingerprint := range compromisedLegacyCAFingerprints {
		if !isCompromisedLegacyCA(fingerprint) {
			t.Fatalf("historical fingerprint %d was accepted", i)
		}
	}
	if isCompromisedLegacyCA([sha256.Size]byte{}) {
		t.Fatal("unrecognized fingerprint was rejected")
	}
}

func TestLoadOrCreateCreatesUniqueSecureCA(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	caA, created, err := LoadOrCreate(filepath.Join(dirA, "ca.crt"), filepath.Join(dirA, "ca.key"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected CA creation")
	}
	caB, _, err := LoadOrCreate(filepath.Join(dirB, "ca.crt"), filepath.Join(dirB, "ca.key"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(caA.PEM, caB.PEM) {
		t.Fatal("separate installations generated identical CAs")
	}
	keyInfo, err := os.Stat(filepath.Join(dirA, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if got := keyInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("key mode = %o, want 600", got)
	}
	if caA.Certificate.Leaf == nil || !caA.Certificate.Leaf.IsCA {
		t.Fatal("generated certificate is not a parsed CA")
	}
}

func TestLoadOrCreateRejectsIncompletePair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(certPath, filepath.Join(dir, "ca.key"), "test"); err == nil {
		t.Fatal("expected incomplete key pair to be rejected")
	}
}

func TestLoadOrCreateLoadsExistingCAAndSecuresKey(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	createdCA, created, err := LoadOrCreate(certPath, keyPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first load did not create a CA")
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	loadedCA, created, err := LoadOrCreate(certPath, keyPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing CA reported as created")
	}
	if !bytes.Equal(createdCA.PEM, loadedCA.PEM) {
		t.Fatal("reloaded CA differs from created CA")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateRejectsKeyWithoutCertificate(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(filepath.Join(dir, "ca.crt"), keyPath, "test"); err == nil {
		t.Fatal("expected incomplete key pair to be rejected")
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("missing certificate", func(t *testing.T) {
		_, err := load(filepath.Join(t.TempDir(), "missing.crt"), "unused")
		if err == nil || !strings.Contains(err.Error(), "read CA certificate") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		dir := t.TempDir()
		certPEM, _, err := generate("test", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		certPath := filepath.Join(dir, "ca.crt")
		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = load(certPath, filepath.Join(dir, "missing.key"))
		if err == nil || !strings.Contains(err.Error(), "read CA private key") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("malformed pair", func(t *testing.T) {
		dir := t.TempDir()
		certPath := filepath.Join(dir, "ca.crt")
		keyPath := filepath.Join(dir, "ca.key")
		if err := os.WriteFile(certPath, []byte("bad cert"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, []byte("bad key"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := load(certPath, keyPath)
		if err == nil || !strings.Contains(err.Error(), "load CA key pair") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("non CA", func(t *testing.T) {
		certPath, keyPath := writeTestCertificate(t, false, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		_, err := load(certPath, keyPath)
		if err == nil || !strings.Contains(err.Error(), "not a certificate authority") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		certPath, keyPath := writeTestCertificate(t, true, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
		_, err := load(certPath, keyPath)
		if err == nil || !strings.Contains(err.Error(), "is not valid") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestWritePairCleansKeyWhenCertificateExists(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePair(certPath, keyPath, []byte("certificate"), []byte("key")); err == nil {
		t.Fatal("expected existing certificate error")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("partial key was not removed: %v", err)
	}
}

func TestWritePairRejectsInvalidDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writePair(filepath.Join(parent, "ca.crt"), filepath.Join(parent, "ca.key"), []byte("cert"), []byte("key"))
	if err == nil || !strings.Contains(err.Error(), "create CA directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteExclusiveRejectsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(path, []byte("new"), 0o600); err == nil {
		t.Fatal("expected exclusive create to fail")
	}
}

func TestExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if found, err := exists(path); err != nil || found {
		t.Fatalf("missing path result = %v, %v", found, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := exists(path); err != nil || !found {
		t.Fatalf("existing path result = %v, %v", found, err)
	}
	if _, err := exists("bad\x00path"); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatalf("invalid path error = %v", err)
	}
}

func writeTestCertificate(t *testing.T, isCA bool, notBefore, notAfter time.Time) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test certificate"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
