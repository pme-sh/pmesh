package vhttp

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/hosts"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/security"
	"get.pme.sh/pmesh/xlog"

	"github.com/samber/lo"
)

type VirtualHostOptions struct {
	Hostnames []string `yaml:"-"`
	NoUpgrade bool     `yaml:"no_upgrade,omitempty"` // Do not upgrade HTTP to HTTPS.
}
type VirtualHost struct {
	VirtualHostOptions
	Mux
}

func NewVirtualHost(opt VirtualHostOptions) (vh *VirtualHost) {
	vh = &VirtualHost{
		VirtualHostOptions: opt,
	}
	return
}

type virtualHostGroup struct {
	hostname string
	hosts    []*VirtualHost
}

func newVirtualHostGroup(hostname string) (vhg *virtualHostGroup) {
	vhg = &virtualHostGroup{
		hostname: hostname,
		hosts:    make([]*VirtualHost, 0, 2),
	}
	return
}
func (vh *virtualHostGroup) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	// If the subdomain is not matched, it is not a match.
	sub, ok := hosts.Match(vh.hostname, r.Host)
	if !ok {
		return Continue
	}

	// With each virtual host, try to handle the request.
	buffer := strings.Builder{}
	prevHostname := "-"
	for _, host := range vh.hosts {
		// If HTTP request & user wants to upgrade to HTTPS, redirect.
		if r.URL.Scheme == "http" && !host.NoUpgrade {
			if _, ok := r.Header["Upgrade-Insecure-Requests"]; ok {
				r.URL.Scheme = "https"
				http.Redirect(w, r, r.URL.String(), http.StatusMovedPermanently)
				return Done
			}
		}

		if hn := host.Hostnames[0]; hn != prevHostname {
			buffer.Reset()
			buffer.WriteString(sub)
			buffer.WriteString(host.Hostnames[0])
			prevHostname = hn
		}
		r.URL.Host = buffer.String()
		result := host.ServeHTTP(w, r)
		r.URL.Host = r.Host
		switch result {
		case Done:
			return Done
		case Drop:
			return Continue
		}
	}
	return Continue
}

func (vhg *virtualHostGroup) add(vh *VirtualHost) {
	vhg.hosts = append(vhg.hosts, vh)
}

type groups struct {
	ordered []*virtualHostGroup
	unique  map[string]*virtualHostGroup
}

func createGroups(vhosts ...*VirtualHost) *groups {
	unique := make(map[string]*virtualHostGroup)
	ordered := make([]*virtualHostGroup, 0, len(vhosts))
	for _, vh := range vhosts {
		for _, hostname := range vh.Hostnames {
			group, ok := unique[hostname]
			if !ok {
				group = newVirtualHostGroup(hostname)
				unique[hostname] = group
				ordered = append(ordered, group)
			}
			group.add(vh)
		}
	}
	return &groups{ordered: ordered, unique: unique}
}

type TopLevelMux struct {
	groups atomic.Pointer[groups]
}

func (mux *TopLevelMux) getGroups() (ordered []*virtualHostGroup, unique map[string]*virtualHostGroup) {
	if g := mux.groups.Load(); g != nil {
		ordered, unique = g.ordered, g.unique
	}
	return
}

// Hostnames returns a list of all hostnames in the mux.
func (mux *TopLevelMux) Hostnames() (hostnames []string) {
	_, unique := mux.getGroups()
	return lo.Keys(unique)
}

// Replaces virtual hosts in bulk.
func (mux *TopLevelMux) SetHosts(vhosts ...*VirtualHost) {
	mux.groups.Store(createGroups(vhosts...))
}

// GetCertificate implements tls.Config.GetCertificate.
func (mux *TopLevelMux) GetCertificate(chi *tls.ClientHelloInfo) (cert *tls.Certificate, err error) {
	_, unique := mux.getGroups()

	var vhg *virtualHostGroup
	if sni := chi.ServerName; sni != "" {
		_, _, vhg = hosts.MatchMap(unique, sni)
		if vhg != nil {
			cert = security.ObtainCertificate(config.Get().Secret, sni).TLS
		}
	}

	if vhg == nil {
		netx.ResetConn(chi.Conn)
		xlog.ErrorC(chi.Context()).Str("sni", chi.ServerName).Str("ip", chi.Conn.RemoteAddr().String()).Msg("rejecting client hello, no matching vhost")
		return nil, fmt.Errorf("invalid server name")
	}
	return
}
