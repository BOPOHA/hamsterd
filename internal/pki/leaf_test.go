package pki

import (
	"crypto/x509"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestIssuer(t *testing.T, maxEntries int) (*Issuer, CA) {
	t.Helper()
	dir := t.TempDir()
	ca, _, err := LoadOrCreate(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), "test")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewIssuer(ca.Certificate, maxEntries)
	if err != nil {
		t.Fatal(err)
	}
	return issuer, ca
}

func TestGeneratedCAConstraints(t *testing.T) {
	_, ca := newTestIssuer(t, 1)
	cert := ca.Certificate.Leaf
	if cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Fatalf("path length = %d zero=%v, want an explicit zero constraint", cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if lifetime := cert.NotAfter.Sub(cert.NotBefore); lifetime > CAValidity+time.Hour {
		t.Fatalf("CA lifetime = %v", lifetime)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("CA extended key usage = %v", cert.ExtKeyUsage)
	}
	if ca.ExpiresSoon(time.Now()) || !ca.ExpiresSoon(cert.NotAfter.Add(-time.Hour)) {
		t.Fatal("ExpiresSoon does not reflect the CA expiry")
	}
}

func TestIssuerSignsVerifiableUniqueLeaves(t *testing.T) {
	issuer, ca := newTestIssuer(t, 4)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate.Leaf)

	first, err := issuer.Certificate("app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Leaf.Verify(x509.VerifyOptions{DNSName: "app.example.com", Roots: roots}); err != nil {
		t.Fatalf("leaf does not verify: %v", err)
	}
	again, err := issuer.Certificate("app.example.com")
	if err != nil || again != first {
		t.Fatalf("cached leaf = (%p, %v), want %p", again, err, first)
	}
	other, err := issuer.Certificate("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Leaf.Verify(x509.VerifyOptions{DNSName: "127.0.0.1", Roots: roots}); err != nil {
		t.Fatalf("IP leaf does not verify: %v", err)
	}
	if first.Leaf.SerialNumber.Cmp(other.Leaf.SerialNumber) == 0 {
		t.Fatal("leaves share a serial number")
	}
	if first.Leaf.SerialNumber.BitLen() < 64 {
		t.Fatalf("serial number has only %d bits", first.Leaf.SerialNumber.BitLen())
	}
	if lifetime := first.Leaf.NotAfter.Sub(first.Leaf.NotBefore); lifetime > LeafValidity+leafBackdate {
		t.Fatalf("leaf lifetime = %v", lifetime)
	}
	if _, err := issuer.Certificate(""); err == nil {
		t.Fatal("empty hostname was signed")
	}
}

func TestIssuerRenewsBeforeExpiryAndEvicts(t *testing.T) {
	issuer, _ := newTestIssuer(t, 1)
	now := time.Now()
	issuer.now = func() time.Time { return now }
	first, err := issuer.Certificate("one.example")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(LeafValidity - leafRenewal + time.Minute)
	renewed, err := issuer.Certificate("one.example")
	if err != nil || renewed == first {
		t.Fatalf("leaf was not renewed: (%p, %v)", renewed, err)
	}
	if _, err := issuer.Certificate("two.example"); err != nil {
		t.Fatal(err)
	}
	if len(issuer.entries) != 1 || issuer.entries["one.example"] != nil {
		t.Fatalf("cache entries = %v", issuer.order)
	}
}

func TestIssuerKeepsLeafCappedByCAExpiry(t *testing.T) {
	issuer, _ := newTestIssuer(t, 1)
	now := issuer.caCert.NotAfter.Add(-24 * time.Hour)
	issuer.now = func() time.Time { return now }
	first, err := issuer.Certificate("one.example")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Leaf.NotAfter.Equal(issuer.caCert.NotAfter) {
		t.Fatalf("leaf expiry %v exceeds CA expiry %v", first.Leaf.NotAfter, issuer.caCert.NotAfter)
	}
	again, err := issuer.Certificate("one.example")
	if err != nil || again != first {
		t.Fatal("leaf capped by CA expiry was regenerated")
	}
}

func TestIssuerGeneratesOncePerHostConcurrently(t *testing.T) {
	issuer, _ := newTestIssuer(t, 8)
	var wg sync.WaitGroup
	var distinct sync.Map
	var count atomic.Int32
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			certificate, err := issuer.Certificate("shared.example")
			if err != nil {
				t.Error(err)
				return
			}
			if _, loaded := distinct.LoadOrStore(certificate, true); !loaded {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("generated %d certificates for one host", count.Load())
	}
}
