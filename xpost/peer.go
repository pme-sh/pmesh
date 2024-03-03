package xpost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/geo"
	"github.com/pme-sh/pmesh/netx"
	"github.com/pme-sh/pmesh/pmtp"
	"github.com/pme-sh/pmesh/security"
)

type Peer struct {
	MachineID string         `json:"machine_id,omitempty"` // the machine ID of the member
	Host      string         `json:"host"`                 // the hostname of the member
	IP        string         `json:"ip"`                   // the IP address of the member
	Lat       float64        `json:"lat" bson:"lat"`       // the latitude and longitude of the member
	Lon       float64        `json:"lon" bson:"lon"`       // the latitude and longitude of the member
	Country   string         `json:"country"`              // the country of the member
	ISP       string         `json:"isp"`                  // the ISP of the member
	Heartbeat int64          `json:"heartbeat"`            // the time of the last heartbeat (ms since epoch)
	Me        bool           `json:"me,omitempty"`         // if this is the local member
	Distance  float64        `json:"distance,omitempty"`   // the distance from the local member (meters)
	UD        map[string]any `json:"ud"`                   // user data
	SD        map[string]any `json:"sd"`                   // system data
}

func FillPeerForSelf(ctx context.Context) Peer {
	ipinfo := netx.GetPublicIPInfo(ctx)
	return Peer{
		MachineID: config.GetMachineID().String(),
		Host:      config.Get().Host,
		IP:        ipinfo.IP.String(),
		Lat:       ipinfo.Lat,
		Lon:       ipinfo.Lon,
		Country:   ipinfo.Country,
		ISP:       ipinfo.Org,
		Heartbeat: time.Now().UnixMilli(),
		Me:        true,
		UD:        config.Get().PeerUD,
	}
}

func (p *Peer) DistanceTo(other *Peer) float64 {
	return geo.LatLon(p.Lat, p.Lon).DistanceTo(geo.LatLon(other.Lat, other.Lon))
}

func (p *Peer) Alive() bool {
	if p.Me {
		return true
	}
	threshold := time.Now().UnixMilli() - HeartbeatTimeout.Milliseconds()
	return p.Heartbeat > threshold
}

const HeartbeatInterval = 30 * time.Second
const HeartbeatTimeout = 1 * time.Hour

func readfrom(body any) (io.Reader, error) {
	switch body := body.(type) {
	case nil, struct{}:
		return nil, nil
	case io.Reader:
		return body, nil
	case string:
		return strings.NewReader(body), nil
	case []byte:
		return bytes.NewReader(body), nil
	case json.RawMessage:
		return bytes.NewReader(body), nil
	default:
		buf := bytes.NewBuffer(nil)
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return nil, err
		}
		return buf, nil
	}
}
func readinto(result any, reader io.Reader) (err error) {
	if result != nil {
		switch result := result.(type) {
		case *json.RawMessage:
			*result, err = io.ReadAll(reader)
		case *[]byte:
			*result, err = io.ReadAll(reader)
		case *string:
			var b []byte
			b, err = io.ReadAll(reader)
			*result = string(b)
		case *struct{}:
		default:
			dec := json.NewDecoder(reader)
			err = dec.Decode(result)
		}
	}
	return
}

var getInternalTransport = sync.OnceValue(func() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 16384,
		IdleConnTimeout:     10 * time.Second,
		TLSClientConfig:     security.CreateMutualAuthenticator(config.Get().Secret, "http/1.1").Client,
	}
})

type InternalTransport struct{}

func (t InternalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return getInternalTransport().RoundTrip(req)
}

var HttpClient = &http.Client{Transport: InternalTransport{}}

func (p *Peer) SendRequest(req *http.Request) (res *http.Response, err error) {
	if req.RequestURI != "" {
		req = req.Clone(req.Context())
		req.RequestURI = ""
	}
	req.Host = req.URL.Host
	if p.Me {
		req.URL.Scheme = "http"
		req.URL.Host = "127.0.0.1"
	} else {
		req.URL.Scheme = "https"
		req.URL.Host = p.IP
	}
	req.Header["User-Agent"] = []string{""}
	delete(req.Header, "Upgrade-Insecure-Requests")
	return HttpClient.Do(req)
}
func (p *Peer) Request(ctx context.Context, method string, path string, body any, result any) error {
	reader, err := readfrom(body)
	if err != nil {
		return err
	}
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "https://")
	if strings.HasPrefix(path, "/") {
		path = fmt.Sprintf("%s.pm3%s", p.Host, path)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+path, reader)
	if err != nil {
		return err
	}
	res, err := p.SendRequest(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", res.StatusCode)
	}
	return readinto(result, res.Body)
}
func (p *Peer) Get(ctx context.Context, path string, result any) error {
	return p.Request(ctx, http.MethodGet, path, nil, result)
}
func (p *Peer) Post(ctx context.Context, path string, body any, result any) error {
	return p.Request(ctx, http.MethodPost, path, body, result)
}
func (p *Peer) Put(ctx context.Context, path string, body any, result any) error {
	return p.Request(ctx, http.MethodPut, path, body, result)
}
func (p *Peer) Delete(ctx context.Context, path string, result any) error {
	return p.Request(ctx, http.MethodDelete, path, nil, result)
}
func (p *Peer) Patch(ctx context.Context, path string, body any, result any) error {
	return p.Request(ctx, http.MethodPatch, path, body, result)
}
func (p *Peer) RTT(ctx context.Context) (ms float64) {
	start := time.Now()
	if p.Get(ctx, "/ping", nil) != nil {
		return -1
	}
	return float64(time.Since(start)) / float64(time.Millisecond)
}

func (p *Peer) Connect() (pmtp.Client, error) {
	url := pmtp.DefaultURL
	if !p.Me {
		url = fmt.Sprintf("pmtp://%s", p.IP)
	}
	return pmtp.Connect(url)
}
func (p *Peer) Call(method string, args any, reply any) error {
	cli, err := p.Connect()
	if err != nil {
		return err
	}
	defer cli.Close()
	return cli.Call(method, args, reply)
}

func MeasureConnectivity(ctx context.Context, peers []Peer) map[string]float64 {
	N := len(peers)
	result := make([]float64, N)
	wg := &sync.WaitGroup{}
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			result[i] = peers[i].RTT(ctx)
			wg.Done()
		}(i)
	}
	wg.Wait()

	m := make(map[string]float64, N)
	for i, p := range peers {
		m[p.MachineID] = result[i]
	}
	return m
}
