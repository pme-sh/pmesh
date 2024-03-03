package urlsigner

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"time"
)

// Defines the full set of options for signing a URL.
type Options struct {
	// The URL to sign.
	URL string `json:"url"`
	// The expiration time of the signed URL.
	Expires *time.Time `json:"expires,omitempty"`
	// Headers to include in the signed URL.
	Headers map[string]string `json:"headers,omitempty"`
	// Headers to include in the request after the signature is verified.
	SecretHeaders map[string]string `json:"secrets,omitempty"`
	// Secret internal location for the URL.
	Rewrite string `json:"rewrite,omitempty"`
}

func normalURL(url string) string {
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")
	if len(url) != 1 {
		url = strings.TrimSuffix(url, "/")
	}
	url, _, _ = strings.Cut(url, "?")
	return url
}

func (o *Options) ToDigest() (d Digest) {
	d.Expires = o.Expires
	d.Headers = o.Headers
	d.SecretHeaders = o.SecretHeaders
	d.Rewrite = normalURL(o.Rewrite)
	return
}

// The signed digest included in the signature.
type Digest struct {
	Expires       *time.Time
	Headers       map[string]string
	SecretHeaders map[string]string
	Rewrite       string
}

func writeString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	buf.WriteByte(0)
}
func readString(buf *bytes.Buffer) (string, error) {
	str, err := buf.ReadString(0)
	if err == io.EOF {
		str = buf.String()
		buf.Reset()
		err = nil
	} else {
		str = str[:len(str)-1]
	}
	return str, err
}
func writeMap(buf *bytes.Buffer, m map[string]string) {
	for k, v := range m {
		if k == "" {
			continue
		}
		writeString(buf, k)
		writeString(buf, v)
	}
	buf.WriteByte(0)
}
func readMap(buf *bytes.Buffer) (map[string]string, error) {
	m := make(map[string]string)
	for {
		k, err := readString(buf)
		if err != nil {
			return nil, err
		}
		if k == "" {
			break
		}
		v, err := readString(buf)
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}
func writeTime(buf *bytes.Buffer, t *time.Time) {
	ms := int64(-1)
	if t != nil {
		ms = t.UnixMilli()
	}
	var b [binary.MaxVarintLen64]byte
	n := binary.PutVarint(b[:], ms)
	buf.Write(b[:n])
}
func readTime(buf *bytes.Buffer) (*time.Time, error) {
	ms, err := binary.ReadVarint(buf)
	if err != nil {
		return nil, err
	}
	if ms < 0 {
		return nil, nil
	}
	t := time.UnixMilli(ms)
	return &t, nil
}
func (d *Digest) MarshalBinary() (data []byte, err error) {
	buf := bytes.NewBuffer(nil)
	writeTime(buf, d.Expires)
	writeMap(buf, d.Headers)
	writeMap(buf, d.SecretHeaders)
	writeString(buf, d.Rewrite)
	return buf.Bytes(), nil
}
func (d *Digest) UnmarshalBinary(data []byte) (err error) {
	buf := bytes.NewBuffer(data)
	if d.Expires, err = readTime(buf); err != nil {
		return
	}
	if d.Headers, err = readMap(buf); err != nil {
		return
	}
	if d.SecretHeaders, err = readMap(buf); err != nil {
		return
	}
	if d.Rewrite, err = readString(buf); err != nil {
		return
	}
	return nil
}
