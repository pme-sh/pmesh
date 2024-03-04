package netx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	HdrASN    = http.CanonicalHeaderKey("P-Asn")
	HdrIP     = http.CanonicalHeaderKey("P-Ip")
	HdrIPGeo  = http.CanonicalHeaderKey("P-Ip-Geo")
	HdrVPN    = http.CanonicalHeaderKey("P-Vpn")
	HdrCF     = http.CanonicalHeaderKey("P-Cf")
	HdrMarked = http.CanonicalHeaderKey("P-Marked")
	HdrRay    = http.CanonicalHeaderKey("X-Ray")
)

type CountryISO [2]byte

func (c *CountryISO) UnmarshalText(text []byte) error {
	if len(text) != 2 {
		return errors.New("invalid country code")
	}
	copy(c[:], text)
	return nil
}
func (c CountryISO) IsValid() bool { return c[0] != 0 && (c[0] != 'X' || c[1] != 'X') }
func (c CountryISO) String() string {
	if !c.IsValid() {
		return "XX"
	}
	return string(c[:])
}

type Flags uint32

const (
	FlagVPN Flags = 1 << iota
	FlagCF
	FlagMarked
)

type IPInfo interface {
	ASN() uint32
	Desc() string
	Country() CountryISO
	Flags() Flags
	Unwrap(any) bool
}

type IPInfoProvider interface {
	LookupContext(context.Context, IP) IPInfo
}
type nullIPInfoProvider struct{}

func (nullIPInfoProvider) LookupContext(context.Context, IP) IPInfo { return NullIPInfo{} }

var NullIPInfoProvider IPInfoProvider = nullIPInfoProvider{}

func SetIPInfoHeaders(headers http.Header, ip string, info IPInfo) {
	headers[HdrIP] = []string{ip}
	headers[HdrASN] = []string{fmt.Sprintf("AS%d %s", info.ASN(), info.Desc())}
	headers[HdrIPGeo] = []string{info.Country().String()}
	fl := info.Flags()
	if fl&FlagVPN != 0 {
		headers[HdrVPN] = []string{"1"}
	}
	if fl&FlagCF != 0 {
		headers[HdrCF] = []string{"1"}
	}
	if fl&FlagMarked != 0 {
		headers[HdrMarked] = []string{"1"}
	}
}

var LocalIPInfoHeaders = http.Header{
	HdrIP:    []string{"127.0.0.1"},
	HdrASN:   []string{"AS0 Local"},
	HdrIPGeo: []string{"XX"},
}

type NullIPInfo struct{}

func (NullIPInfo) ASN() uint32         { return 0 }
func (NullIPInfo) Desc() string        { return "" }
func (NullIPInfo) Country() CountryISO { return CountryISO{} }
func (NullIPInfo) Flags() Flags        { return 0 }
func (NullIPInfo) Unwrap(any) bool     { return false }

func NormalizeOrg(k string) string {
	k = strings.ToUpper(k)
	k = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-':
			return '-'
		case ',', '.', '_', '"', '\'':
			return -1
		}

		return r
	}, k)
	return k
}
