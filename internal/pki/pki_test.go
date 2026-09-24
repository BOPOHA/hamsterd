package pki

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
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
