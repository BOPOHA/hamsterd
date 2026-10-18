package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

const (
	// LeafValidity is short because leaves are issued on demand and cached
	// only in memory.
	LeafValidity = 30 * 24 * time.Hour
	// leafRenewal regenerates a cached leaf before clients see it expire.
	leafRenewal  = 7 * 24 * time.Hour
	leafBackdate = time.Hour
)

// Issuer signs per-host TLS server certificates with a CA and caches them in
// memory. Each leaf has its own random key and serial number.
type Issuer struct {
	ca     tls.Certificate
	caCert *x509.Certificate
	max    int
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*leafEntry
	order   []string
}

type leafEntry struct {
	ready       chan struct{}
	certificate *tls.Certificate
	notAfter    time.Time
	err         error
}

func NewIssuer(ca tls.Certificate, maxEntries int) (*Issuer, error) {
	caCert := ca.Leaf
	if caCert == nil {
		if len(ca.Certificate) == 0 {
			return nil, errors.New("CA certificate is missing")
		}
		parsed, err := x509.ParseCertificate(ca.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse CA certificate: %w", err)
		}
		caCert = parsed
	}
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &Issuer{
		ca:      ca,
		caCert:  caCert,
		max:     maxEntries,
		now:     time.Now,
		entries: make(map[string]*leafEntry),
	}, nil
}

// Certificate returns a cached or newly issued certificate for hostname.
// Concurrent callers for the same hostname share one generation; callers for
// different hostnames do not wait for each other.
func (issuer *Issuer) Certificate(hostname string) (*tls.Certificate, error) {
	for {
		issuer.mu.Lock()
		entry := issuer.entries[hostname]
		if entry != nil {
			select {
			case <-entry.ready:
				if entry.err == nil && issuer.fresh(entry.notAfter) {
					issuer.mu.Unlock()
					return entry.certificate, nil
				}
				issuer.removeLocked(hostname)
				entry = nil
			default:
			}
		}
		if entry != nil {
			issuer.mu.Unlock()
			<-entry.ready
			if entry.err != nil {
				return nil, entry.err
			}
			continue
		}
		entry = &leafEntry{ready: make(chan struct{})}
		if len(issuer.order) >= issuer.max {
			issuer.removeLocked(issuer.order[0])
		}
		issuer.entries[hostname] = entry
		issuer.order = append(issuer.order, hostname)
		issuer.mu.Unlock()

		entry.certificate, entry.notAfter, entry.err = issuer.sign(hostname)
		close(entry.ready)
		if entry.err != nil {
			issuer.mu.Lock()
			if issuer.entries[hostname] == entry {
				issuer.removeLocked(hostname)
			}
			issuer.mu.Unlock()
			return nil, entry.err
		}
		return entry.certificate, nil
	}
}

// fresh reports whether a cached leaf can still be served. Leaves capped by
// the CA expiry cannot be renewed into a longer lifetime, so they are kept
// until they actually expire.
func (issuer *Issuer) fresh(notAfter time.Time) bool {
	now := issuer.now()
	if !now.Before(notAfter) {
		return false
	}
	return now.Add(leafRenewal).Before(notAfter) || !notAfter.Before(issuer.caCert.NotAfter)
}

func (issuer *Issuer) removeLocked(hostname string) {
	delete(issuer.entries, hostname)
	for i, name := range issuer.order {
		if name == hostname {
			issuer.order = append(issuer.order[:i], issuer.order[i+1:]...)
			break
		}
	}
}

func (issuer *Issuer) sign(hostname string) (*tls.Certificate, time.Time, error) {
	if hostname == "" {
		return nil, time.Time{}, errors.New("certificate hostname is empty")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("generate leaf serial number: %w", err)
	}
	now := issuer.now()
	notAfter := now.Add(LeafValidity)
	if notAfter.After(issuer.caCert.NotAfter) {
		notAfter = issuer.caCert.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"hamsterd local development proxy"}},
		NotBefore:    now.Add(-leafBackdate),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(hostname); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{hostname}
		template.Subject.CommonName = hostname
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.caCert, &key.PublicKey, issuer.ca.PrivateKey)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("sign leaf certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse leaf certificate: %w", err)
	}
	return &tls.Certificate{
		Certificate: [][]byte{der, issuer.ca.Certificate[0]},
		PrivateKey:  key,
		Leaf:        leaf,
	}, notAfter, nil
}
