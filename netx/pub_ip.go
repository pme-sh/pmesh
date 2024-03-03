package netx

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/config"

	"github.com/samber/lo"
)

type PublicIPInfo struct {
	IP      IP      `json:"ip"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Org     string  `json:"org"`
}

// https://ipinfo.io/json
type ipInfoJson struct {
	IP      IP     `json:"ip"`
	Country string `json:"country"`
	Loc     string `json:"loc"` // lat,lon
	Org     string `json:"org"`
}

func (i ipInfoJson) convert() (r PublicIPInfo, err error) {
	r.IP = i.IP
	r.Country = i.Country
	_, err = fmt.Sscanf(i.Loc, "%f,%f", &r.Lat, &r.Lon)
	r.Org = i.Org
	return
}

var ipInfoJsonRf = newRemoteParsedFile[ipInfoJson]("https://ipinfo.io/json", config.AsnDir.File("ipinfo.io.json"))

// https://ip.guide/frontend/api
type ipGuideJsonAS struct {
	ASN          int    `json:"asn"`
	Name         string `json:"name"`
	Organization string `json:"organization"`
	Country      string `json:"country"`
}
type ipGuideJsonNetwork struct {
	CIDR string        `json:"cidr"`
	AS   ipGuideJsonAS `json:"autonomous_system"`
}
type ipGuideJsonLocation struct {
	City     string  `json:"city"`
	Country  string  `json:"country"`
	Timezone string  `json:"timezone"`
	Lat      float64 `json:"latitude"`
	Lon      float64 `json:"longitude"`
}
type ipGuideJson struct {
	Info struct {
		IP       IP                  `json:"ip"`
		Network  ipGuideJsonNetwork  `json:"network"`
		Location ipGuideJsonLocation `json:"location"`
	} `json:"ip_response"`
}

func (i ipGuideJson) convert() (r PublicIPInfo, err error) {
	r.IP = i.Info.IP
	r.Country = i.Info.Network.AS.Country
	r.Lat = i.Info.Location.Lat
	r.Lon = i.Info.Location.Lon
	r.Org = i.Info.Network.AS.Organization
	return
}

var ipGuideJsonRf = newRemoteParsedFile[ipGuideJson]("https://ip.guide/frontend/api", config.AsnDir.File("ip.guide.json"))

// http://ip-api.com/json
type ipApiJson struct {
	Status  string  `json:"status,omitempty"`
	Country string  `json:"countryCode,omitempty"`
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Org     string  `json:"as,omitempty"`
	IP      IP      `json:"query"`
}

func (i ipApiJson) convert() (r PublicIPInfo, err error) {
	if i.Status != "success" {
		err = fmt.Errorf("status: %s", i.Status)
		return
	}
	r.IP = i.IP
	r.Country = i.Country
	r.Lat = i.Lat
	r.Lon = i.Lon
	r.Org = i.Org
	return
}

var ipApiJsonRf = newRemoteParsedFile[ipApiJson]("http://ip-api.com/json", config.AsnDir.File("ip-api.com.json"))

// https://ipapi.co/json
type ipApiCoJson struct {
	IP      IP      `json:"ip"`
	Country string  `json:"country"`
	Lat     float64 `json:"latitude"`
	Lon     float64 `json:"longitude"`
	Org     string  `json:"org"`
}

func (i ipApiCoJson) convert() (r PublicIPInfo, err error) {
	r.IP = i.IP
	r.Country = i.Country
	r.Lat = i.Lat
	r.Lon = i.Lon
	r.Org = i.Org
	return
}

var ipApiCoJsonRf = newRemoteParsedFile[ipApiCoJson]("https://ipapi.co/json", config.AsnDir.File("ipapi.co.json"))

var ipInfoQuorumCache atomic.Pointer[PublicIPInfo]

func GetPublicIPInfo(c context.Context) (r PublicIPInfo) {
	if ptr := ipInfoQuorumCache.Load(); ptr != nil {
		return *ptr
	}
	sub, cancel := context.WithTimeout(c, 3*time.Second)
	defer cancel()

	type ipconvertable interface {
		convert() (r PublicIPInfo, err error)
	}
	type ipresult struct {
		r ipconvertable
		e error
	}

	wg := &sync.WaitGroup{}
	res := make([]ipresult, 4)
	wg.Add(4)
	go func() {
		defer wg.Done()
		res[0].r, res[0].e = ipInfoJsonRf.LoadContext(sub)
	}()
	go func() {
		defer wg.Done()
		res[1].r, res[1].e = ipApiJsonRf.LoadContext(sub)
	}()
	go func() {
		defer wg.Done()
		res[2].r, res[2].e = ipApiCoJsonRf.LoadContext(sub)
	}()
	go func() {
		defer wg.Done()
		res[3].r, res[3].e = ipGuideJsonRf.LoadContext(sub)
	}()
	wg.Wait()

	outbound := GetOutboundIP()
	results := make([]PublicIPInfo, 0, len(res))
	for _, r := range res {
		if r.e == nil {
			if c, e := r.r.convert(); e == nil {
				if c.IP.IsPublic() {
					if !outbound.IsPublic() || c.IP == outbound {
						if strings.HasPrefix(c.Org, "AS") {
							_, c.Org, _ = strings.Cut(c.Org, " ")
						}
						c.Org = NormalizeOrg(c.Org)
						results = append(results, c)
					}
				}
			}
		}
	}

	if len(results) == 0 {
		return PublicIPInfo{
			IP:      outbound,
			Country: "XX",
			Org:     "Unknown",
		}
	}

	quorum := func(data PublicIPInfo, value func(info PublicIPInfo) any) float64 {
		r := 0.0
		for _, b := range results {
			a := value(data)
			b := value(b)
			switch a := a.(type) {
			case complex128:
				r -= math.Abs(real(a) - real(b.(complex128)))
				r -= math.Abs(imag(a) - imag(b.(complex128)))
			case string:
				if a == b.(string) {
					r++
				}
			case IP:
				if a == b.(IP) {
					r++
				}
			default:
				panic("unexpected type")
			}
		}
		return r
	}

	r.Country = lo.MaxBy(results, func(a, b PublicIPInfo) bool {
		field := func(info PublicIPInfo) any { return info.Country }
		return quorum(a, field) > quorum(b, field)
	}).Country
	r.IP = lo.MaxBy(results, func(a, b PublicIPInfo) bool {
		field := func(info PublicIPInfo) any { return info.IP }
		return quorum(a, field) > quorum(b, field)
	}).IP
	r.Org = lo.MaxBy(results, func(a, b PublicIPInfo) bool {
		field := func(info PublicIPInfo) any { return info.Org }
		return quorum(a, field) > quorum(b, field)
	}).Org
	geo := lo.MaxBy(results, func(a, b PublicIPInfo) bool {
		field := func(info PublicIPInfo) any { return complex(info.Lat, info.Lon) }
		return quorum(a, field) > quorum(b, field)
	})
	r.Lat, r.Lon = geo.Lat, geo.Lon

	if len(results) > 1 {
		ipInfoQuorumCache.Store(&r)
	}
	return
}
