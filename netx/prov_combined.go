package netx

import "context"

type CombinedIPInfo struct {
	OrgPrimary, GeoPrimary IPInfo
}

func (c CombinedIPInfo) ASN() uint32 {
	r := c.OrgPrimary.ASN()
	if r == 0 {
		r = c.GeoPrimary.ASN()
	}
	return r
}
func (c CombinedIPInfo) Desc() string {
	r := c.OrgPrimary.Desc()
	if r == "" {
		r = c.GeoPrimary.Desc()
	}
	return r
}
func (c CombinedIPInfo) Country() CountryISO {
	r := c.GeoPrimary.Country()
	if r[0] == 0 {
		r = c.GeoPrimary.Country()
	}
	return r
}
func (c CombinedIPInfo) Flags() Flags {
	return c.OrgPrimary.Flags() | c.GeoPrimary.Flags()
}
func (c CombinedIPInfo) Unwrap(v any) bool {
	return c.OrgPrimary.Unwrap(v) || c.GeoPrimary.Unwrap(v)
}
func CombineIPInfo(org, geo IPInfo) IPInfo {
	if _, ok := org.(NullIPInfo); ok {
		return geo
	}
	if _, ok := geo.(NullIPInfo); ok {
		return org
	}
	return CombinedIPInfo{OrgPrimary: org, GeoPrimary: geo}
}

type CombinedProvider struct {
	OrgPrimary, GeoPrimary IPInfoProvider
}

func (c CombinedProvider) LookupContext(ctx context.Context, ip IP) IPInfo {
	return CombineIPInfo(
		c.OrgPrimary.LookupContext(ctx, ip),
		c.GeoPrimary.LookupContext(ctx, ip),
	)
}
