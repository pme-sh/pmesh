package pmtp

import (
	"bytes"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xlog"

	"github.com/gorilla/websocket"
)

type ServerProtocol[Arg any] interface {
	Serve(conn net.Conn, arg Arg)
	String() string
}

type UpgradeServer[Proto ServerProtocol[Arg], Arg any] struct {
	Websocket websocket.Upgrader
	Protocols map[string]Proto
}

func MakeUpgradeServer[Proto ServerProtocol[Arg], Arg any](protos ...Proto) UpgradeServer[Proto, Arg] {
	u := UpgradeServer[Proto, Arg]{
		Protocols: make(map[string]Proto),
		Websocket: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
		},
	}
	for _, proto := range protos {
		u.Websocket.Subprotocols = append(u.Websocket.Subprotocols, proto.String())
		u.Protocols[proto.String()] = proto
	}
	return u
}

func (u *UpgradeServer[Proto, Arg]) Upgrade(w http.ResponseWriter, r *http.Request, arg Arg) {
	if r.Method != "GET" {
		vhttp.Error(w, r, http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Connection") != "Upgrade" {
		vhttp.Error(w, r, http.StatusBadRequest)
		return
	}

	upgrade := r.Header.Get("Upgrade")

	// If we can upgrade natively, do so.
	if proto, ok := u.Protocols[upgrade]; ok {
		rc := http.NewResponseController(w)
		conn, _, err := rc.Hijack()
		if err != nil {
			xlog.WarnC(r.Context()).Err(err).EmbedObject(xlog.EnhanceRequest(r)).Msg("hijack failed")
			vhttp.Error(w, r, http.StatusInternalServerError)
			return
		}
		conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n"))
		conn.Write([]byte("Upgrade: " + r.Header.Get("Upgrade") + "\r\n"))
		conn.Write([]byte("Connection: Upgrade\r\n\r\n"))

		// Serve the protocol.
		defer conn.Close()
		proto.Serve(conn, arg)
		return
	}

	// If we can upgrade over websocket, do so.
	if upgrade == "websocket" {
		wc, wcerr := u.Websocket.Upgrade(w, r, nil)
		if wcerr != nil {
			xlog.WarnC(r.Context()).Err(wcerr).EmbedObject(xlog.EnhanceRequest(r)).Msg("websocket upgrade failed")
			return
		}

		// Check for the negotiated subprotocol.
		proto, ok := u.Protocols[wc.Subprotocol()]
		if !ok {
			defer wc.Close()
			xlog.WarnC(r.Context()).Str("subprotocol", wc.Subprotocol()).EmbedObject(xlog.EnhanceRequest(r)).Msg("unsupported subprotocol")
			wc.SetReadDeadline(time.Now().Add(5 * time.Second))
			wc.CloseHandler()(websocket.CloseProtocolError, "unsupported subprotocol")
			return
		}

		// Serve the protocol.
		conn := wsrwc{conn: wc}
		defer conn.Close()
		proto.Serve(&conn, arg)
		return
	}

	// If we can't upgrade, return an error.
	xlog.WarnC(r.Context()).Str("protocol", upgrade).EmbedObject(xlog.EnhanceRequest(r)).Msg("unsupported protocol")
	vhttp.Error(w, r, http.StatusNotAcceptable)
}

// Protocol erased listener that forwards connections to a channel.

type erasedProtocol struct {
	backlog chan net.Conn
	proto   string
}

func (a erasedProtocol) Serve(conn net.Conn, arg any) {
	select {
	case a.backlog <- conn:
	case <-time.After(5 * time.Second):
		xlog.Warn().Msg("Listener backlog full")
		conn.Close()
	}
}
func (a erasedProtocol) String() string {
	return a.proto
}

type UpgradeListener struct {
	backlog chan net.Conn
	UpgradeServer[erasedProtocol, any]
}

func NewUpgradeListener(name string) *UpgradeListener {
	ch := make(chan net.Conn, 1024)
	return &UpgradeListener{
		backlog:       ch,
		UpgradeServer: MakeUpgradeServer(erasedProtocol{ch, name}),
	}
}

func (u *UpgradeListener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.Upgrade(w, r, nil)
}
func (u *UpgradeListener) Accept() (net.Conn, error) {
	conn, ok := <-u.backlog
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}
func (u *UpgradeListener) Close() error {
	close(u.backlog)
	return nil
}
func (u *UpgradeListener) Addr() net.Addr {
	return nil
}

// Wrapper for websocket.Conn to implement net.Conn.
type wsrwc struct {
	conn   *websocket.Conn
	closed atomic.Bool
	buf    bytes.Buffer
}

func (w *wsrwc) LocalAddr() net.Addr {
	return w.conn.LocalAddr()
}
func (w *wsrwc) RemoteAddr() net.Addr {
	return w.conn.RemoteAddr()
}
func (w *wsrwc) SetDeadline(t time.Time) error {
	return w.conn.NetConn().SetDeadline(t)
}
func (w *wsrwc) SetReadDeadline(t time.Time) error {
	return w.conn.SetReadDeadline(t)
}
func (w *wsrwc) SetWriteDeadline(t time.Time) error {
	return w.conn.SetWriteDeadline(t)
}

func (w *wsrwc) Write(in []byte) (n int, err error) {
	return len(in), w.conn.WriteMessage(websocket.TextMessage, in)
}
func (w *wsrwc) Read(out []byte) (n int, err error) {
	// If there's data in the buffer, return it.
	n, _ = w.buf.Read(out)
	if n > 0 || len(out) == 0 {
		return n, nil
	}

	for {
		// Otherwise, read from the websocket.
		ty, in, err := w.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		if ty != websocket.TextMessage && ty != websocket.BinaryMessage {
			continue
		} else if len(in) == 0 {
			continue
		}

		n = copy(out, in)
		// If there's still data in the message, put it back in the buffer.
		if len(in) > n {
			w.buf.Write(in[n:])
		}
		return n, nil
	}
}
func (w *wsrwc) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	go func() {
		defer w.conn.Close()
		w.conn.CloseHandler()(websocket.CloseNormalClosure, "")
	}()
	return nil
}
