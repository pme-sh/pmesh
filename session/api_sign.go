package session

import (
	"net/http"

	"get.pme.sh/pmesh/urlsigner"
)

func init() {
	Match("/sign", func(session *Session, r *http.Request, p urlsigner.Options) (res string, err error) {
		return session.Server.Signer.Sign(p)
	})
}
