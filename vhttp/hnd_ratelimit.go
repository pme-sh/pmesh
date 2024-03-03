package vhttp

import (
	"net/http"
	"strings"

	"github.com/pme-sh/pmesh/rate"
	"github.com/pme-sh/pmesh/variant"
)

type RateLimitHandler struct {
	rate.Limit
}

func (rh RateLimitHandler) String() string {
	return rh.Limit.String()
}
func (rh *RateLimitHandler) UnmarshalInline(text string) error {
	if before, ok := strings.CutPrefix(text, "limit "); ok {
		return rh.Limit.UnmarshalText([]byte(before))
	}
	return variant.RejectMatch(rh)
}

func (rh RateLimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	if hnd := EnforceRateReq(r, rh.Limit); hnd != nil {
		hnd.ServeHTTP(w, r)
		return Done
	}
	return Continue
}

func init() {
	Registry.Define("Limit", func() any { return &RateLimitHandler{} })
}
