package netx

import (
	"bytes"
	"context"
	"io"

	"github.com/pme-sh/pmesh/config"
)

const (
	cf4URL = "https://www.cloudflare.com/ips-v4"
	cf6URL = "https://www.cloudflare.com/ips-v6"
)

type cloudfareInfoDecoded struct {
	Network IPNet `xsv:"x"`
}

var cfname = NormalizeOrg("Cloudflare, Inc.")

type cloudflareInfo struct{ NullIPInfo }

func (c cloudflareInfo) Flags() Flags { return FlagCF }
func (c cloudflareInfo) ASN() uint32  { return 13335 }
func (c cloudflareInfo) Desc() string {
	return cfname
}

type cfList struct {
	IPMap[cloudflareInfo]
}

type cloudflareProvider struct {
	v4 *remoteParsedFile[cfList]
	v6 *remoteParsedFile[cfList]
}

// https://developers.cloudflare.com/email-routing/postmaster/#outbound-prefixes
var fixedV4 = ParseIPNet("104.30.0.0/20")
var fixedV6 = ParseIPNet("2405:8100:c000::/38")

func (m cloudflareProvider) LookupContext(c context.Context, ip IP) IPInfo {
	if ip.IsV4() {
		if fixedV4.Contains(ip) {
			return cloudflareInfo{}
		} else if data, err := m.v4.LoadContext(c); err == nil && data != nil {
			if r := data.Find(ip); r != nil {
				return r
			}
		}
	} else {
		if fixedV6.Contains(ip) {
			return cloudflareInfo{}
		} else if data, err := m.v6.LoadContext(c); err == nil && data != nil {
			if r := data.Find(ip); r != nil {
				return r
			}
		}
	}
	return NullIPInfo{}
}

var CloudflareProvider IPInfoProvider = &cloudflareProvider{
	newRemoteParsedFile[cfList](cf4URL, config.AsnDir.File("cf-ips-v4.txt")),
	newRemoteParsedFile[cfList](cf6URL, config.AsnDir.File("cf-ips-v6.txt")),
}

func (i2 *cfList) UnmarshalBinary(data []byte) error {
	d := newXsvDecoder(bytes.NewReader(data), "\t", "x")
	info := &cloudflareInfo{}
	for {
		var block cloudfareInfoDecoded
		if err := d.Decode(&block); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		i2.Add(info, block.Network.IP, block.Network.Limit())
	}
	return nil
}
