package netx

import (
	"context"
	"strings"
)

type MarkerProvider struct {
	inner IPInfoProvider
	list  [256][]string
}

func NewMarkerProvider(inner IPInfoProvider, list []string) *MarkerProvider {
	prov := &MarkerProvider{inner: inner}
	for _, s := range list {
		s = NormalizeOrg(s)
		i := s[0]
		prov.list[i] = append(prov.list[i], s)
	}
	return prov
}

type markerInfo struct {
	i IPInfo
}

func (s markerInfo) ASN() uint32         { return s.i.ASN() }
func (s markerInfo) Desc() string        { return s.i.Desc() }
func (s markerInfo) Country() CountryISO { return s.i.Country() }
func (s markerInfo) Flags() Flags        { return s.i.Flags() | FlagMarked }
func (s markerInfo) Unwrap(v any) bool   { return s.i.Unwrap(v) }

func (s *MarkerProvider) LookupContext(ctx context.Context, ip IP) (info IPInfo) {
	info = s.inner.LookupContext(ctx, ip)
	if desc := info.Desc(); desc != "" {
		for _, s := range s.list[desc[0]] {
			if strings.HasPrefix(desc, s) {
				info = markerInfo{info}
				break
			}
		}
	}
	return
}
