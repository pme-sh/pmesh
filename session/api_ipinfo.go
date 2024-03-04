package session

import (
	"errors"
	"net/http"

	"get.pme.sh/pmesh/netx"
)

type IPInfoResult struct {
	IP      string `json:"ip"`
	ASN     uint32 `json:"asn"`
	Desc    string `json:"desc"`
	Country string `json:"country"`
	Flags   uint32 `json:"flags"`
}
type IPInfoQuery struct {
	IP string `json:"ip"`
}

func init() {
	Match("/ipinfo", func(s *Session, r *http.Request, i IPInfoQuery) (res IPInfoResult, err error) {
		ip := netx.ParseIP(i.IP)
		if ip.IsZero() {
			err = errors.New("invalid ip")
			return
		}

		res.IP = ip.String()
		info := s.Server.GetIPInfoProvider().LookupContext(r.Context(), ip)
		if info != nil {
			res.ASN = info.ASN()
			res.Desc = info.Desc()
			res.Country = info.Country().String()
			res.Flags = uint32(info.Flags())
		}
		return
	})
}
