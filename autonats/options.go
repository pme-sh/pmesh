package autonats

import (
	"cmp"
	"crypto/tls"
	"net"
	"strings"

	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/security"
	"get.pme.sh/pmesh/tlsmux"
	"get.pme.sh/pmesh/xlog"

	"github.com/nats-io/nats.go"
	"github.com/samber/lo"
)

type Options struct {
	Debug       bool
	ServerName  string // Name of the server
	ClusterName string // Name of the cluster
	StoreDir    string // Directory to store data

	LocalAddr string // Address we bind to for strictly local connections
	LocalPort int    // Port to bind to for strictly local connections

	Addr      string // Address we bind to
	Advertise string // Address we advertise as
	Port      int    // Port to bind to for remote connections

	Secret    string       // Secret for interserver communication
	Logger    *xlog.Logger // Logger to use
	TLSConfig *tls.Config  // TLS configuration

	Topology Topology // Topology to use for bootstrapping
}

func NewTLSConfig(secret string) (tlsc *tls.Config) {
	mutauth := security.GetSelfSignedRootCA(secret + "-n")
	sni := security.GetSecretCNSuffix(secret)
	sub := lo.Must(mutauth.IssueCertificate("-", sni))
	return &tls.Config{
		RootCAs:                  mutauth.ToCertPool(),
		ClientCAs:                mutauth.ToCertPool(),
		Certificates:             []tls.Certificate{*sub.TLS},
		ServerName:               sni,
		ClientAuth:               tls.RequireAndVerifyClientCert,
		PreferServerCipherSuites: true,
		CurvePreferences:         []tls.CurveID{tls.CurveP256, tls.X25519},
	}
}

func (opts *Options) SetDefaults() {
	opts.Addr = cmp.Or(opts.Addr, "0.0.0.0")
	opts.Port = cmp.Or(opts.Port, 8443)
	opts.LocalAddr = cmp.Or(opts.LocalAddr, "127.0.0.1")
	if opts.TLSConfig == nil {
		opts.TLSConfig = NewTLSConfig(opts.Secret)
	}
	if opts.Logger == nil {
		opts.Logger = xlog.NewDomain("nats")
	}
	if opts.Advertise == "" {
		if outbound := netx.GetOutboundIP(); outbound.IsPublic() {
			opts.Advertise = outbound.String()
		}
	}
}

type dialer func(n, a string) (net.Conn, error)

func (d dialer) SkipTLSHandshake() bool {
	return true
}
func (d dialer) Dial(network, address string) (net.Conn, error) {
	return d(network, address)
}

func WithDialFunc(d func(network, address string) (net.Conn, error)) nats.Option {
	return nats.SetCustomDialer(dialer(d))
}
func WithSystemAccount() nats.Option {
	return nats.UserInfo("sys", "sys")
}
func WithAutoDialer(secret string) nats.Option {
	tlsc := NewTLSConfig(secret)
	return func(o *nats.Options) error {
		o.CustomDialer = dialer(func(n, a string) (net.Conn, error) {
			return tlsmux.Dial(n, a, tlsc, "nats-client")
		})
		o.SkipHostLookup = true
		o.TLSConfig = nil
		return nil
	}
}

func Connect(hosts []string, secret string, opts ...nats.Option) (*nats.Conn, error) {
	opts = append(opts,
		func(o *nats.Options) error {
			for _, host := range hosts {
				if host, ok := strings.CutPrefix(host, "nats://"); ok {
					o.Servers = append(o.Servers, host)
					continue
				}
				host = strings.TrimPrefix(host, "pmtp://")
				if strings.IndexByte(host, ':') != -1 {
					o.Servers = append(o.Servers, host)
				} else {
					o.Servers = append(o.Servers, net.JoinHostPort(host, "8443"))
				}
			}
			return nil
		},
		WithAutoDialer(secret),
	)
	return nats.Connect("", opts...)
}
