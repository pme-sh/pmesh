package netx

import (
	"context"
	"net/http"
	"strings"
)

type Proxier string

const (
	ProxierNone       Proxier = ""
	ProxierCloudflare Proxier = "cloudflare"
	ProxierGeneric    Proxier = "generic"
)

var (
	hdrCfConnectingIP = http.CanonicalHeaderKey("CF-Connecting-IP")
	hdrCfCountry      = http.CanonicalHeaderKey("CF-IPCountry")
	hdrXForwardedFor  = http.CanonicalHeaderKey("X-Forwarded-For")
)

type ProxyTraits struct {
	Proxier     Proxier // Proxier type
	Edge        IP      // Parsed value from http.Request.RemoteAddr
	Origin      IP      // Original client
	CountryHint CountryISO
}

func findFirstPublicIPInForwardList(values []string) IP {
	for _, value := range values {
		ip := ParseIP(strings.TrimSpace(value))
		if ip.IsPublic() {
			return ip
		}
	}
	return IP{}
}

func IsCloudflareIP(ip IP) (bool, error) {
	res := CloudflareProvider.LookupContext(context.Background(), ip)
	return (res.Flags() & FlagCF) == FlagCF, nil
}

func ResolveProxyTraits(request *http.Request) (traits ProxyTraits) {
	addrPort := ParseIPPort(request.RemoteAddr)
	traits.Edge = addrPort.IP
	if traits.Edge.IsZero() {
		panic("invalid remote address")
	}
	traits.Origin = traits.Edge

	// Trust X-Forwarded-For only if the origin is loopback or private
	if values, ok := request.Header[hdrXForwardedFor]; ok {
		if traits.Origin.IsLoopback() || traits.Origin.IsPrivate() {
			if adr := findFirstPublicIPInForwardList(values); !adr.IsZero() {
				traits.Proxier = ProxierGeneric
				traits.Origin = adr
			}
		}
	}

	// Trust CF-Connecting-IP only if the origin is cloudflare
	if values, ok := request.Header[hdrCfConnectingIP]; ok {
		if iscf, e := IsCloudflareIP(traits.Origin); e == nil && iscf {
			if adr := findFirstPublicIPInForwardList(values); !adr.IsZero() {
				traits.Proxier = ProxierCloudflare
				traits.Origin = adr
				if values := request.Header[hdrCfCountry]; len(values) == 1 {
					if v := values[0]; len(v) == 2 {
						traits.CountryHint = CountryISO{v[0], v[1]}
					}
				}
			}
		}
	}
	return
}
