package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

var compromisedLegacyCAFingerprints = [...][sha256.Size]byte{
	{
		0x69, 0x18, 0x35, 0x60, 0x2a, 0x47, 0x04, 0xba,
		0x77, 0xed, 0x31, 0xfa, 0x39, 0x0c, 0xb3, 0x95,
		0x64, 0xd8, 0xbb, 0xde, 0x10, 0x29, 0x49, 0x2c,
		0xee, 0xc8, 0xae, 0x6d, 0xb5, 0x24, 0x22, 0x07,
	},
	{
		0x35, 0xaa, 0xfc, 0xdb, 0xd8, 0x3f, 0x64, 0x26,
		0x46, 0x78, 0x3d, 0x49, 0xc3, 0x63, 0x46, 0x90,
		0x37, 0x43, 0xec, 0x17, 0x43, 0x5a, 0x5f, 0x4c,
		0x37, 0x85, 0xf0, 0x2f, 0xc1, 0x07, 0xf8, 0x1f,
	},
	{
		0xaf, 0x73, 0x9a, 0x82, 0x99, 0xc3, 0x61, 0x17,
		0xb9, 0x10, 0x5c, 0x28, 0x41, 0xf4, 0xb7, 0xf3,
		0x64, 0x36, 0x7d, 0x3b, 0xb3, 0xab, 0xda, 0x72,
		0x15, 0xfb, 0x9e, 0xec, 0x18, 0x84, 0x1d, 0x6f,
	},
}

type CA struct {
	Certificate tls.Certificate
	PEM         []byte
}

func LoadOrCreate(certPath, keyPath, name string) (CA, bool, error) {
	certExists, err := exists(certPath)
	if err != nil {
		return CA{}, false, err
	}
	keyExists, err := exists(keyPath)
	if err != nil {
		return CA{}, false, err
	}
	if certExists != keyExists {
		return CA{}, false, errors.New("CA certificate and private key must either both exist or both be absent")
	}
	if !certExists {
		certPEM, keyPEM, err := generate(name, time.Now())
		if err != nil {
			return CA{}, false, err
		}
		if err := writePair(certPath, keyPath, certPEM, keyPEM); err != nil {
			return CA{}, false, err
		}
	}
	ca, err := load(certPath, keyPath)
	if err != nil {
		return CA{}, false, err
	}
	return ca, !certExists, nil
}

func load(certPath, keyPath string) (CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return CA{}, fmt.Errorf("read CA certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return CA{}, fmt.Errorf("read CA private key: %w", err)
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return CA{}, fmt.Errorf("load CA key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return CA{}, fmt.Errorf("parse CA certificate: %w", err)
	}
	if !leaf.IsCA || leaf.KeyUsage&x509.KeyUsageCertSign == 0 {
		return CA{}, errors.New("certificate is not a certificate authority")
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	if isCompromisedLegacyCA(fingerprint) {
		return CA{}, errors.New("refusing a public legacy CA; remove it from trust stores, delete the old CA files, and restart to generate a unique CA")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return CA{}, fmt.Errorf("CA certificate is not valid at %s", now.Format(time.RFC3339))
	}
	certificate.Leaf = leaf
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return CA{}, fmt.Errorf("secure CA private key: %w", err)
	}
	return CA{Certificate: certificate, PEM: certPEM}, nil
}

func isCompromisedLegacyCA(fingerprint [sha256.Size]byte) bool {
	for _, compromised := range compromisedLegacyCAFingerprints {
		if fingerprint == compromised {
			return true
		}
	}
	return false
}

func generate(name string, now time.Time) ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA private key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"hamsterd local development proxy"},
			CommonName:   name + " local CA",
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode CA private key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func writePair(certPath, keyPath string, certPEM, keyPEM []byte) error {
	for _, dir := range []string{filepath.Dir(certPath), filepath.Dir(keyPath)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create CA directory: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure CA directory: %w", err)
		}
	}
	if err := writeExclusive(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write CA private key: %w", err)
	}
	if err := writeExclusive(certPath, certPEM, 0o644); err != nil {
		_ = os.Remove(keyPath)
		return fmt.Errorf("write CA certificate: %w", err)
	}
	return nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect %q: %w", path, err)
}
