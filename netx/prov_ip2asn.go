package netx

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"time"

	"get.pme.sh/pmesh/config"
)

const (
	ip2asnURL = "https://iptoasn.com/data/ip2asn-combined.tsv.gz"
)

type IP2ASNBlock struct {
	IPStart                IP         `xsv:"ip_start"`
	IPEnd                  IP         `xsv:"ip_end"`
	AutonomousSystemNumber uint32     `xsv:"asn"`
	CountryCode            CountryISO `xsv:"country_code"`
	OrganizationName       string     `xsv:"asn_description"`
}

func (i *IP2ASNBlock) ASN() uint32 {
	return i.AutonomousSystemNumber
}
func (i *IP2ASNBlock) Desc() string {
	return i.OrganizationName
}
func (i *IP2ASNBlock) Country() CountryISO {
	return i.CountryCode
}
func (i *IP2ASNBlock) Flags() Flags {
	return 0
}
func (i *IP2ASNBlock) Unwrap(v any) bool {
	switch v := v.(type) {
	case *IP2ASNBlock:
		*v = *i
		return true
	}
	return false
}

type IP2ASN struct {
	Set IPMap[IP2ASNBlock]
}

type ip2AsnProvider struct {
	*remoteParsedFile[IP2ASN]
}

func (m ip2AsnProvider) LookupContext(c context.Context, ip IP) IPInfo {
	if data, err := m.LoadContext(c); err == nil && data != nil {
		if r := data.Set.Find(ip); r != nil {
			return r
		}
	}
	return NullIPInfo{}
}

var IP2ASNProvider IPInfoProvider = &ip2AsnProvider{
	newRemoteParsedFile[IP2ASN](ip2asnURL, config.AsnDir.File("ip2asn-combined.tsv.gz")),
}

func (i2 *IP2ASN) UnmarshalBinary(data []byte) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer reader.Close()

	t0 := time.Now()
	defer func() {
		fmt.Println("ip2asn unmarshal", time.Since(t0))
	}()
	d := newXsvDecoder(reader, "\t", "ip_start", "ip_end", "asn", "country_code", "asn_description")
	for {
		var block IP2ASNBlock
		if err := d.Decode(&block); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if block.AutonomousSystemNumber != 0 {
			block.OrganizationName = NormalizeOrg(block.OrganizationName)
			i2.Set.Add(&block, block.IPStart, block.IPEnd)
		}
	}
	return nil
}
