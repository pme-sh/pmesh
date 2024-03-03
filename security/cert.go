package security

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"
)

const useRSA = false

func newKeyPair(rng io.Reader) (pub any, priv any, err error) {
	if useRSA {
		key, e := rsa.GenerateKey(rng, 2048)
		if e != nil {
			return nil, nil, e
		}
		return &key.PublicKey, key, e
	} else {
		curve := elliptic.P256()
		key := make([]byte, 32)
		rng.Read(key)
		secret := new(big.Int).SetBytes(key)
		pk := &ecdsa.PrivateKey{D: secret}
		pk.PublicKey.Curve = curve
		pk.PublicKey.X, pk.PublicKey.Y = curve.ScalarBaseMult(secret.Bytes())
		return &pk.PublicKey, pk, nil
	}
}

type Certificate struct {
	CertPEM    []byte
	KeyPEM     []byte
	TLS        *tls.Certificate
	X509       *x509.Certificate
	PrivateKey any
}

// Returns true if the certificate is self-signed.
func (c *Certificate) IsSelfsigned() (bool, error) {
	return c.X509.Issuer.String() == c.X509.Subject.String(), nil
}

// Returns an error if the certificate is not trusted by the system.
func (c *Certificate) Verify() error {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil
	}
	_, err = c.X509.Verify(x509.VerifyOptions{Roots: roots})
	return err
}

// Installs the certificate into the system root CA store.
func (c *Certificate) InstallRoot() error {
	tmp, err := os.CreateTemp("", "pmesh_*.crt")
	if err != nil {
		return err
	}
	certPath := tmp.Name()
	defer os.Remove(certPath)
	_, err = tmp.Write(c.CertPEM)
	if err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	switch runtime.GOOS {
	case "windows":
		return exec.Command("certutil", "-addstore", "-f", "ROOT", certPath).Run()
	case "darwin":
		return exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certPath).Run()
	case "freebsd":
		fallthrough
	case "linux":
		hash := sha1.New()
		hash.Write(c.CertPEM)
		fileName := fmt.Sprintf("pmesh_%s.crt", hex.EncodeToString(hash.Sum(nil)))
		e1 := os.WriteFile("/usr/local/share/ca-certificates/"+fileName, c.CertPEM, 0644)
		e2 := os.WriteFile("/etc/pki/ca-trust/source/anchors/"+fileName, c.CertPEM, 0644)
		e3 := os.WriteFile("/etc/ssl/certs/"+fileName, c.CertPEM, 0644)
		if e1 != nil && e2 != nil && e3 != nil {
			return fmt.Errorf("failed to write certificate to system store: %v", e1)
		}
		e1 = exec.Command("update-ca-certificates").Run()
		e2 = exec.Command("update-ca-trust").Run()
		e3 = exec.Command("certctl", "rehash").Run()
		if e1 != nil && e2 != nil && e3 != nil {
			return fmt.Errorf("failed to update certificate store %v", e1)
		}
		return nil

	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// Returns a cert pool with only this certificate.
func (c *Certificate) ToCertPool() (pool *x509.CertPool) {
	pool = x509.NewCertPool()
	pool.AddCert(c.X509)
	return
}

// Writes the certificate and key to the given paths.
func (c *Certificate) Save(certPath, keyPath string) error {
	err := os.WriteFile(keyPath, c.KeyPEM, 0600)
	if err == nil {
		err = os.WriteFile(certPath, c.CertPEM, 0644)
	}
	return err
}

// Issues a certificate signed by us that is valid for the given hosts.
func (parent *Certificate) IssueCertificate(cn string, hosts ...string) (cert *Certificate, err error) {
	// Initialize the CPRNG, generate a private key
	cprng := NewCipherCprng(parent.KeyPEM)
	for _, h := range hosts {
		cprng.Associate([]byte(h))
	}
	derivedPub, derivedPriv, err := newKeyPair(cprng)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	// Create the certificate
	cert = &Certificate{}
	err = cert.create(
		cprng,
		cn, hosts,
		derivedPub, derivedPriv,
		parent.X509, parent.PrivateKey,
	)
	return
}

// Loads a certificate from the given paths.
func LoadCertificate(certPath, keyPath string) (cert *Certificate, err error) {
	cert = &Certificate{}
	cert.CertPEM, err = os.ReadFile(certPath)
	if err == nil {
		cert.KeyPEM, err = os.ReadFile(keyPath)
		if err == nil {
			err = cert.finalize()
		}
	}
	return
}

// Generates new root certificate given a secret and common name.
func NewSelfSignedRootCA(secret []byte, cn string) (cert *Certificate, err error) {
	cprng := NewCipherCprng(secret)
	pub, priv, err := newKeyPair(cprng)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	// Create the certificate
	cert = &Certificate{}
	err = cert.create(
		cprng,
		cn, []string{},
		pub, priv,
		nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create root certificate: %v", err)
	}
	return cert, nil
}

func (c *Certificate) finalize() error {
	cert, err := tls.X509KeyPair(c.CertPEM, c.KeyPEM)
	if err != nil {
		return err
	}
	c.TLS = &cert

	decoded, _ := pem.Decode(c.CertPEM)
	if decoded == nil {
		return fmt.Errorf("no certificate found")
	}
	if c.X509, err = x509.ParseCertificate(decoded.Bytes); err != nil {
		return err
	}

	decoded, _ = pem.Decode(c.KeyPEM)
	if decoded == nil {
		return fmt.Errorf("no key found")
	}
	c.PrivateKey, err = x509.ParsePKCS8PrivateKey(decoded.Bytes)
	return err
}
func (c *Certificate) createFromTemplates(rng io.Reader, template *x509.Certificate, pub any, priv any, certIssuer *x509.Certificate, privIssuer any) error {
	certPemBuffer := bytes.NewBuffer(nil)
	derBytes, err := x509.CreateCertificate(rng, template, certIssuer, pub, privIssuer)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %v", err)
	}
	if err := pem.Encode(certPemBuffer, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed encode certificate: %v", err)
	}
	c.CertPEM = certPemBuffer.Bytes()

	keyPemBuffer := bytes.NewBuffer(nil)
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("unable to marshal private key: %v", err)
	}
	if err := pem.Encode(keyPemBuffer, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed encode key: %v", err)
	}
	c.KeyPEM = keyPemBuffer.Bytes()
	return c.finalize()
}
func (c *Certificate) create(rng io.Reader, cn string, hosts []string, pub any, priv any, certIssuer *x509.Certificate, privIssuer any) error {
	var serial [16]byte
	_, e := rng.Read(serial[:])
	if e != nil {
		return e
	}

	tmp := x509.Certificate{
		NotBefore:             time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2323, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	tmp.SerialNumber = new(big.Int).SetBytes(serial[:])
	tmp.Subject = pkix.Name{
		Organization: []string{cn},
		CommonName:   cn,
	}
	if _, ok := pub.(*rsa.PublicKey); ok {
		tmp.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmp.IPAddresses = append(tmp.IPAddresses, ip)
		} else {
			tmp.DNSNames = append(tmp.DNSNames, h)
			tmp.DNSNames = append(tmp.DNSNames, "*."+h)
		}
	}
	if certIssuer == nil {
		certIssuer = &tmp
		privIssuer = priv
	}
	return c.createFromTemplates(rng, &tmp, pub, priv, certIssuer, privIssuer)
}
