package security

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/pme-sh/pmesh/netx"
	"github.com/pme-sh/pmesh/xlog"

	"github.com/samber/lo"
)

// Gets the protocol name used as an oracle to verify client knowledge before
// allowing a connection to be established with the internal SNI.
func GetClientAuthOracle(secret string) string {
	return base64.RawURLEncoding.EncodeToString(
		GenerateKey(secret, "cli-proto", 5),
	)
}

type MutualAuthenticator struct {
	Client *tls.Config
	Server *tls.Config
	Oracle string
}

func (m MutualAuthenticator) GetConfigForClient(chi *tls.ClientHelloInfo) (*tls.Config, error) {
	// If server name is not "pm3", we don't need to do anything
	if chi.ServerName != "pm3" && !strings.HasSuffix(chi.ServerName, ".pm3") {
		return nil, nil
	}

	// If local connection, we don't need to do anything either
	ipp := netx.ParseIPPort(chi.Conn.RemoteAddr().String())
	if ipp.IP.IsLoopback() {
		return nil, nil
	}

	// Call the GetCertificate method to verify side-channel oracle
	_, err := m.Server.GetCertificate(chi)
	if err != nil {
		return nil, err
	}
	return m.Server, nil
}

func (m MutualAuthenticator) WrapServer(tcfg *tls.Config) *tls.Config {
	if prev := tcfg.GetConfigForClient; prev == nil {
		tcfg.GetConfigForClient = m.GetConfigForClient
	} else {
		tcfg.GetConfigForClient = func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			cfg, err := m.GetConfigForClient(chi)
			if cfg != nil || err != nil {
				return cfg, err
			}
			return prev(chi)
		}
	}
	return tcfg
}

// Creates a TLS config for a client and server that uses mutual authentication.
func CreateMutualAuthenticator(secret string, protocols ...string) (m MutualAuthenticator) {
	clientCert := GetSelfSignedClientCA(secret)
	m.Oracle = GetClientAuthOracle(secret)
	m.Client = &tls.Config{
		RootCAs:      GetSelfSignedRootCA(secret).ToCertPool(),
		Certificates: []tls.Certificate{*clientCert.TLS},
		ServerName:   "pm3",
		NextProtos:   append([]string{m.Oracle}, protocols...),
	}
	m.Server = &tls.Config{
		NextProtos:               protocols,
		ClientAuth:               tls.RequireAndVerifyClientCert,
		ClientCAs:                clientCert.ToCertPool(),
		PreferServerCipherSuites: true,
		CurvePreferences:         []tls.CurveID{tls.CurveP256, tls.X25519},
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if chi.ServerName != "pm3" && !strings.HasSuffix(chi.ServerName, ".pm3") {
				defer netx.ResetConn(chi.Conn)
				xlog.Warn().
					Stringer("addr", chi.Conn.RemoteAddr()).
					Str("host", chi.ServerName).
					Msg("rejecting client hello, server name mismatch")
				return nil, fmt.Errorf("invalid server name")
			}
			if !lo.Contains(chi.SupportedProtos, m.Oracle) {
				defer netx.ResetConn(chi.Conn)
				xlog.Warn().
					Stringer("addr", chi.Conn.RemoteAddr()).
					Str("host", chi.ServerName).
					Any("protos", chi.SupportedProtos).
					Msg("rejecting client hello, oracle mismatch")
				return nil, fmt.Errorf("invalid oracle protocol")
			}
			return ObtainCertificate(secret, chi.ServerName).TLS, nil
		},
	}
	return
}
