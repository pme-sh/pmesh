package session

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"get.pme.sh/pmesh/concurrent"
	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/pmtp"
	"get.pme.sh/pmesh/snowflake"
	"get.pme.sh/pmesh/xlog"
	"get.pme.sh/pmesh/xpost"
)

type LambdaID struct {
	MachineID config.MachineID
	UID       snowflake.ID
}

func NewLambdaID() LambdaID {
	return LambdaID{
		MachineID: config.GetMachineID(),
		UID:       snowflake.New(),
	}
}

var lambdaIDEncoding = base64.RawURLEncoding

func (l LambdaID) MarshalBinary() ([]byte, error) {
	var data [12]byte
	binary.LittleEndian.PutUint32(data[:4], uint32(l.MachineID))
	binary.LittleEndian.PutUint64(data[4:], uint64(l.UID))
	return data[:], nil
}
func (l *LambdaID) UnmarshalBinary(data []byte) error {
	if len(data) != 12 {
		return errors.New("invalid LambdaID length")
	}
	l.MachineID = config.MachineID(binary.LittleEndian.Uint32(data[:4]))
	l.UID = snowflake.ID(binary.LittleEndian.Uint64(data[4:]))
	return nil
}
func (l LambdaID) MarshalText() ([]byte, error) {
	data, err := l.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return lambdaIDEncoding.AppendEncode(nil, data), nil
}
func (l *LambdaID) UnmarshalText(data []byte) error {
	data, err := lambdaIDEncoding.DecodeString(string(data))
	if err != nil {
		return err
	}
	return l.UnmarshalBinary(data)
}
func (l LambdaID) String() string {
	data, _ := l.MarshalText()
	return string(data)
}
func (l LambdaID) IsZero() bool {
	return l.MachineID.Uint32() == 0
}
func (l LambdaID) Peer(session *Session) *xpost.Peer {
	if l.IsZero() {
		return nil
	}
	return session.Peerlist.Find(l.MachineID.String())
}

type Lambda struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Code      string      `json:"code"`
	Headers   http.Header `json:"headers"`

	client pmtp.Client
}

var lambdaRegistrations = concurrent.Map[snowflake.ID, *Lambda]{}

func init() {
	type LambdaProps struct {
		req *http.Request
		id  LambdaID
	}
	lambdaServer := pmtp.MakeRPCServer(func(conn net.Conn, code pmtp.Code, o LambdaProps) {
		cli, err := code.Open(conn)
		if err != nil {
			xlog.WarnC(o.req.Context()).Err(err).Msg("Failed to open")
			return
		}
		defer cli.Close()
		err = cli.Call("open", o.id.String(), nil)
		if err != nil {
			xlog.WarnC(o.req.Context()).Err(err).Msg("Failed to register lambda")
			return
		}

		// Store the client in the map, and remove it when the connection is closed.
		lambdaRegistrations.Store(o.id.UID, &Lambda{
			ID:        o.id.String(),
			Timestamp: o.id.UID.Timestamp(),
			Code:      code.String(),
			Headers:   o.req.Header,

			client: cli,
		})
		defer lambdaRegistrations.Delete(o.id.UID)

		// Check health every 30 seconds
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if cli.Busy() == -1 {
				break
			}
		}
	})

	// Register the lambda router.
	ApiRouter.HandleFunc("GET /lambda/new", func(w http.ResponseWriter, r *http.Request) {
		id := NewLambdaID()
		lambdaServer.Upgrade(w, r, LambdaProps{r, id})
	})
	ApiRouter.HandleFunc("GET /lambda/new/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := LambdaID{}
		err := id.UnmarshalText([]byte(r.PathValue("id")))
		if err != nil {
			writeOutput(r, w, nil, err)
			return
		}
		if id.MachineID != config.GetMachineID() {
			writeOutput(r, w, nil, errors.New("not a local lambda"))
			return
		}
		lambdaServer.Upgrade(w, r, LambdaProps{r, id})
	})

	// Register the lambda API.
	ApiRouter.HandleFunc("/lambda/{id}/{method}", func(w http.ResponseWriter, r *http.Request) {
		// Parse the lambda ID.
		id := LambdaID{}
		err := id.UnmarshalText([]byte(r.PathValue("id")))
		if err != nil {
			writeOutput(r, w, nil, err)
			return
		}

		// If this is a local lambda, call it directly.
		if id.MachineID == config.GetMachineID() {
			lambda, ok := lambdaRegistrations.Load(id.UID)
			if !ok {
				writeOutput(r, w, nil, errors.New("lambda not found"))
				return
			}

			var body any
			var reply json.RawMessage
			err = parseInput(&body, w, r)
			if err == nil {
				err = lambda.client.Call(r.PathValue("method"), body, &reply)
			}
			writeOutput(r, w, reply, err)
			return
		}

		// Find the peer
		session := RequestSession(r)
		peer := id.Peer(session)
		if peer == nil {
			writeOutput(r, w, nil, errors.New("peer not found"))
			return
		}

		// Create a new request with a timeout
		context, cancel := context.WithTimeout(r.Context(), ApiRequestMaxDuration)
		defer cancel()
		req := r.Clone(context)
		req.RequestURI = ""
		req.URL.Path = "/lambda/" + id.String() + "/" + r.PathValue("method")

		// Forward the request to the peer
		res, err := peer.SendRequest(req)
		if err != nil {
			writeOutput(r, w, nil, err)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	})

	// List all lambdas
	Match("/lambda", func(session *Session, r *http.Request, p struct{}) (res map[string]*Lambda, _ error) {
		res = make(map[string]*Lambda)
		lambdaRegistrations.Range(func(_ snowflake.ID, v *Lambda) bool {
			res[v.ID] = v
			return true
		})
		return
	})
}
