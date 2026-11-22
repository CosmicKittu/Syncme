// Package identity manages the PC's long-term TLS key.
// The device ID is the hex sha256 of the certificate's public key (SPKI),
// which is what phones pin.
package identity

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
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Identity struct {
	Key  *ecdsa.PrivateKey
	Cert tls.Certificate
	SPKI []byte
	ID   string
}

func IDFromSPKI(spki []byte) string {
	s := sha256.Sum256(spki)
	return hex.EncodeToString(s[:])
}

// Short formats the first 16 hex chars as XXXX-XXXX-XXXX-XXXX.
func Short(id string) string {
	if len(id) < 16 {
		return strings.ToUpper(id)
	}
	u := strings.ToUpper(id[:16])
	return u[0:4] + "-" + u[4:8] + "-" + u[8:12] + "-" + u[12:16]
}

func LoadOrCreate(dir string) (*Identity, error) {
	keyPath := filepath.Join(dir, "key.pem")
	certPath := filepath.Join(dir, "cert.pem")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if err := create(keyPath, certPath); err != nil {
			return nil, err
		}
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("identity key is not ECDSA")
	}
	cert.Leaf = leaf
	return &Identity{Key: key, Cert: cert, SPKI: leaf.RawSubjectPublicKeyInfo, ID: IDFromSPKI(leaf.RawSubjectPublicKeyInfo)}, nil
}

func create(keyPath, certPath string) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "syncme"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(30, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"syncme"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}
