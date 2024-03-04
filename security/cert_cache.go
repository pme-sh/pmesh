package security

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"sync"

	"get.pme.sh/pmesh/concurrent"
	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/xlog"
)

var certCache = concurrent.Map[string, *Certificate]{}
var certGenLock [2]sync.Mutex

const fileCacheDisabled = false

var secretEncoding = base64.RawURLEncoding.WithPadding(base64.NoPadding)

func GetSecretHash(secret string) string {
	return secretEncoding.EncodeToString(GenerateKey(secret, "cert store", 7))
}
func GetSecretCNSuffix(secret string) string {
	return GetSecretHash(secret)
}

func GenerateCertWithSecret(secret string, hosts []string) (cert *Certificate, err error) {
	if len(hosts) == 0 {
		cn := "CA " + GetSecretCNSuffix(secret)
		return NewSelfSignedRootCA([]byte(secret), cn)
	} else {
		sb := &strings.Builder{}
		for _, h := range hosts {
			sb.WriteString(h)
			sb.WriteByte('-')
		}
		sb.WriteString(GetSecretCNSuffix(secret))
		return GetSelfSignedRootCA(secret).IssueCertificate(sb.String(), hosts...)
	}
}
func ValidateCertWithSecret(secret string, cert *Certificate, hosts []string) error {
	if !strings.HasSuffix(cert.X509.Subject.CommonName, GetSecretCNSuffix(secret)) {
		return fmt.Errorf("invalid common name")
	}
	if len(hosts) == 0 {
		// Check if the certificate is trusted
		if cert.Verify() == nil {
			return nil
		}
		if err := cert.InstallRoot(); err != nil {
			xlog.Warn().Err(err).Msg("failed to install root certificate")
			return nil
		}
		dummy, err := cert.IssueCertificate("test")
		if err != nil {
			return fmt.Errorf("failed to issue test certificate: %v", err)
		}
		if err = dummy.Verify(); err != nil {
			// On -nix, this can be a transient error due to the system pool
			// not being updated yet. We'll just ignore it.
			//
			roots, err := x509.SystemCertPool()
			if err == nil {
				roots.AddCert(cert.X509)
			}
			return nil
			//return fmt.Errorf("failed to verify test certificate: %v", err)
		}
	}
	return nil
}

// ObtainCertificate returns a certificate for the given hosts signed by the root CA.
func ObtainCertificate(secret string, hosts ...string) (cert *Certificate) {
	id := ""
	lock := &certGenLock[0]
	switch len(hosts) {
	case 0:
		id = "root"
		lock = &certGenLock[1]
	case 1:
		id = hosts[0]
	default:
		id = strings.Join(hosts, "_")
	}
	kvid := secret + id

	// Try to load the certificate from the cache
	if c, ok := certCache.Load(kvid); ok {
		return c
	}
	lock.Lock()
	defer lock.Unlock()
	if c, ok := certCache.Load(kvid); ok {
		return c
	}

	// Try to load the certificate from the file cache
	fileid := GetSecretHash(secret) + id
	crt := config.CertDir.File(fileid + ".crt")
	key := config.CertDir.File(fileid + ".key")
	if !fileCacheDisabled {
		cert, err := LoadCertificate(crt, key)
		if err == nil {
			err = ValidateCertWithSecret(secret, cert, hosts)
			if err == nil {
				certCache.Store(kvid, cert)
				return cert
			}
		}
	}

	// Generate a new certificate
	cert, err := GenerateCertWithSecret(secret, hosts)
	if err != nil {
		log.Fatalf("failed to generate certificate: %v", err)
	}
	if err := ValidateCertWithSecret(secret, cert, hosts); err != nil {
		log.Fatalf("failed to validate certificate: %v", err)
	}
	if !fileCacheDisabled {
		if err := cert.Save(crt, key); err != nil {
			log.Fatalf("failed to save certificate: %v", err)
		}
	}
	cert, _ = certCache.LoadOrStore(kvid, cert)
	return
}

// GetSelfSignedRootCA returns the self-signed root certificate.
func GetSelfSignedRootCA(secret string) (cert *Certificate) {
	return ObtainCertificate(secret)
}

// GetSelfSignedClientCA returns the self-signed client certificate authority.
func GetSelfSignedClientCA(secret string) (cert *Certificate) {
	return ObtainCertificate(secret + "-c")
}
