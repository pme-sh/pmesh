package urlsigner

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

var ErrInvalidSignature = errors.New("invalid signature")
var ErrCorruptSignature = errors.New("corrupt signature")
var ErrExpiredSignature = errors.New("expired signature")
var ErrHeaderMismatch = errors.New("header mismatch")
var HdrSignature = "X-Psn"
var QuerySignature = "psn"

type Signer struct {
	Key []byte // 16-byte key for AES-128-GCM
}

func New(secret string) *Signer {
	sha := sha1.New()
	sha.Write([]byte(secret))
	sha.Write([]byte("vhttp.URLSigner"))
	return &Signer{Key: sha.Sum(nil)[:16]}
}
func (s *Signer) GCM() (gcm cipher.AEAD, err error) {
	block, err := aes.NewCipher(s.Key)
	if err != nil {
		return
	}
	return cipher.NewGCM(block)
}
func (s *Signer) RawSign(blob []byte, ad []byte) (string, error) {
	gcm, err := s.GCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, blob, ad)), nil
}
func (s *Signer) RawAuthenticate(signature string, ad []byte) ([]byte, error) {
	gcm, err := s.GCM()
	if err != nil {
		return nil, err
	}
	bin, err := base64.RawURLEncoding.DecodeString(signature)
	nonce := gcm.NonceSize()
	if err != nil || len(bin) < nonce {
		return nil, err
	}

	return gcm.Open(nil, bin[:nonce], bin[nonce:], ad)
}

func (s *Signer) Sign(o Options) (string, error) {
	digest := o.ToDigest()
	ad := normalURL(o.URL)
	bin, err := digest.MarshalBinary()
	if err != nil {
		return "", err
	}
	return s.RawSign(bin, []byte(ad))
}
func (s *Signer) Authenticate(r *http.Request) (signed bool, err error) {
	// Skip if not signed.
	signature := ""
	if sig := r.Header[HdrSignature]; len(sig) == 1 {
		signature = sig[0]
	} else if sq := r.URL.Query().Get(QuerySignature); sq != "" {
		signature = sq
	}
	if signature == "" {
		return false, nil
	}

	// Parse the signature.
	ad := normalURL(r.URL.String())
	bin, err := s.RawAuthenticate(signature, []byte(ad))
	if err != nil {
		return false, ErrInvalidSignature
	}
	var digest Digest
	if err := digest.UnmarshalBinary(bin); err != nil {
		return false, ErrCorruptSignature
	}

	// Check expiration.
	if digest.Expires != nil && digest.Expires.Before(time.Now()) {
		return false, ErrExpiredSignature
	}

	// Check headers.
	for k, v := range digest.Headers {
		if r.Header.Get(k) != v {
			return false, ErrHeaderMismatch
		}
	}

	// Add secret headers.
	for k, v := range digest.SecretHeaders {
		r.Header.Set(k, v)
	}

	// Rewrite the URL.
	if digest.Rewrite != "" {
		host, path, found := strings.Cut(digest.Rewrite, "/")
		if found && host != "" {
			r.Host = host
			r.URL.Host = host
			r.URL.Path = "/" + path
		} else {
			r.URL.Path = "/" + digest.Rewrite
		}
	}
	return true, nil
}
