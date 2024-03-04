package netx

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/xlog"
)

const (
	geoLite2GeoURL = "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country-CSV&suffix=zip&license_key=%s"
	geoLite2AsnURL = "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-ASN-CSV&suffix=zip&license_key=%s"
)

type GeoLite2Location struct {
	ID             uint32     `xsv:"geoname_id"`
	CountryISOCode CountryISO `xsv:"country_iso_code"`
	//LocaleCode        string     `xsv:"locale_code"`
	//ContinentCode     string     `xsv:"continent_code"`
	//ContinentName     string     `xsv:"continent_name"`
	//CountryName       string     `xsv:"country_name"`
	//IsInEuropeanUnion bool `xsv:"is_in_european_union"`
}
type GeoLite2CountryBlock struct {
	Network              IPNet             `xsv:"network"`
	LocationID           uint32            `xsv:"geoname_id"`
	RegisteredCountryID  uint32            `xsv:"registered_country_geoname_id"`
	RepresentedCountryID uint32            `xsv:"represented_country_geoname_id"`
	IsAnonymousProxy     bool              `xsv:"is_anonymous_proxy"`
	Location             *GeoLite2Location `xsv:"-"`
	//IsSatelliteProvider  bool              `xsv:"is_satellite_provider"`
	//IsAnycast            bool              `xsv:"is_anycast"`
}
type GeoLite2ASNBlock struct {
	Network                IPNet  `xsv:"network"`
	AutonomousSystemNumber int    `xsv:"autonomous_system_number"`
	OrganizationName       string `xsv:"autonomous_system_organization"`
}

type combinedInfoBlocks struct {
	country *GeoLite2CountryBlock
	asn     *GeoLite2ASNBlock
}

func (c combinedInfoBlocks) Unwrap(v any) bool {
	switch v := v.(type) {
	case *GeoLite2CountryBlock:
		if c.country != nil {
			*v = *c.country
			return true
		}
	case *GeoLite2ASNBlock:
		if c.asn != nil {
			*v = *c.asn
			return true
		}
	}
	return false
}

func (c combinedInfoBlocks) ASN() uint32 {
	if c.asn != nil {
		return uint32(c.asn.AutonomousSystemNumber)
	}
	return 0
}
func (c combinedInfoBlocks) Desc() string {
	if c.asn != nil {
		return c.asn.OrganizationName
	}
	return ""
}
func (c combinedInfoBlocks) Country() CountryISO {
	if c.country != nil && c.country.Location != nil {
		return c.country.Location.CountryISOCode
	}
	return CountryISO{}
}
func (c combinedInfoBlocks) Flags() Flags {
	var flags Flags
	if c := c.country; c != nil {
		if c.IsAnonymousProxy {
			flags |= FlagVPN
		}
	}
	return flags
}

type GeoLite2Country struct {
	Locations map[uint32]*GeoLite2Location
	Set       IPMap[GeoLite2CountryBlock]
}
type GeoLite2ASN struct {
	Set IPMap[GeoLite2ASNBlock]
}

type MaxmindProvider struct {
	geo *remoteParsedFile[GeoLite2Country]
	asn *remoteParsedFile[GeoLite2ASN]
}

func NewMaxmindProvider(key string) MaxmindProvider {
	return MaxmindProvider{
		geo: newRemoteParsedFile[GeoLite2Country](fmt.Sprintf(geoLite2GeoURL, key), config.AsnDir.File("GeoLite2-Country-CSV.zip")),
		asn: newRemoteParsedFile[GeoLite2ASN](fmt.Sprintf(geoLite2AsnURL, key), config.AsnDir.File("GeoLite2-ASN-CSV.zip")),
	}
}
func (m MaxmindProvider) LookupContext(c context.Context, ip IP) IPInfo {
	info := &combinedInfoBlocks{}
	if geo, err := m.geo.LoadContext(c); err == nil {
		if geo == nil {
			return NullIPInfo{}
		}
		info.country = geo.Set.Find(ip)
	} else {
		xlog.WarnC(c).Err(err).Msg("maxmind geo loading failed")
	}
	if asn, err := m.asn.LoadContext(c); err == nil {
		if asn == nil {
			return NullIPInfo{}
		}
		info.asn = asn.Set.Find(ip)
	} else {
		xlog.WarnC(c).Err(err).Msg("maxmind asn loading failed")
	}
	return info
}

type parseOrder struct {
	name string
	dec  func(*xsvDecoder) error
}

func csvZipParseAll(data []byte, orders []parseOrder) error {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, order := range orders {
		found := false
		for _, file := range zipReader.File {
			if path.Base(file.Name) == order.name {
				cfile, err := file.Open()
				if err != nil {
					return err
				}
				defer cfile.Close()
				if err := order.dec(newXsvDecoder(cfile, ",")); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing %s", order.name)
		}
	}
	return nil
}
func (g2 *GeoLite2ASN) UnmarshalBinary(data []byte) (e error) {
	asnBlockParser := func(d *xsvDecoder) error {
		for {
			var block GeoLite2ASNBlock
			if err := d.Decode(&block); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			block.OrganizationName = NormalizeOrg(block.OrganizationName)
			g2.Set.Add(&block, block.Network.IP, block.Network.Limit())
		}
		return nil
	}
	return csvZipParseAll(data, []parseOrder{
		{"GeoLite2-ASN-Blocks-IPv4.csv", asnBlockParser},
		{"GeoLite2-ASN-Blocks-IPv6.csv", asnBlockParser},
	})
}
func (g2 *GeoLite2Country) UnmarshalBinary(data []byte) (e error) {
	g2.Locations = make(map[uint32]*GeoLite2Location, 256)
	countryBlockParser := func(d *xsvDecoder) error {
		for {
			var block GeoLite2CountryBlock
			if err := d.Decode(&block); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}

			if loc, ok := g2.Locations[block.RepresentedCountryID]; ok {
				block.Location = loc
			} else if loc, ok = g2.Locations[block.LocationID]; ok {
				block.Location = loc
			} else if loc, ok = g2.Locations[block.RegisteredCountryID]; ok {
				block.Location = loc
			}
			g2.Set.Add(&block, block.Network.IP, block.Network.Limit())
		}
		return nil
	}
	locationsParser := func(d *xsvDecoder) error {
		for {
			var block GeoLite2Location
			if err := d.Decode(&block); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			g2.Locations[block.ID] = &block
		}
		return nil
	}
	return csvZipParseAll(data, []parseOrder{
		{"GeoLite2-Country-Locations-en.csv", locationsParser},
		{"GeoLite2-Country-Blocks-IPv4.csv", countryBlockParser},
		{"GeoLite2-Country-Blocks-IPv6.csv", countryBlockParser},
	})
}
