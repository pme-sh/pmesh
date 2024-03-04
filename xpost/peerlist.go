package xpost

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/hosts"
	"get.pme.sh/pmesh/xlog"
)

// System data source
type SDSource = func(in map[string]any)

// Peerlist type
type Peerlist struct {
	gw *enats.Gateway

	mu     sync.RWMutex
	cancel context.CancelFunc
	last   []Peer
	err    error
	errn   int
	self   Peer

	sds  sync.Map // map[int32]SDSource
	sdsn atomic.Int32
}

func NewPeerlist(gw *enats.Gateway) *Peerlist {
	return &Peerlist{gw: gw}
}

func (m *Peerlist) AddSDSource(sds ...SDSource) {
	for _, s := range sds {
		m.sds.Store(m.sdsn.Add(1), s)
	}
}

func (m *Peerlist) update(ctx context.Context, self Peer) ([]Peer, error) {
	kv := m.gw.PeerKV

	self.SD = make(map[string]any)
	m.sds.Range(func(_, v any) bool {
		v.(SDSource)(self.SD)
		return true
	})
	self.Heartbeat = time.Now().UnixMilli()
	copyForMarshal := Peer(self)
	copyForMarshal.MachineID = ""
	copyForMarshal.Me = false
	copyForMarshal.Distance = 0
	data, err := json.Marshal(copyForMarshal)
	if err != nil {
		return nil, err
	}
	_, err = kv.Put(ctx, self.MachineID, data)
	if err != nil {
		return nil, err
	}

	keys, err := kv.Keys(ctx)
	if err != nil {
		return nil, err
	}

	peers := make([]Peer, 0, len(keys))
	for _, k := range keys {
		data, err := kv.Get(ctx, k)
		if err != nil {
			return nil, err
		}
		var p Peer
		if json.Unmarshal(data.Value(), &p) == nil {
			p.MachineID = k
			p.Me = p.MachineID == self.MachineID
			p.Distance = p.DistanceTo(&self)
			peers = append(peers, p)
		}
	}

	// Sort by ascending heartbeat so that the one with the higher heartbeat will override the
	// one with the lower heartbeat. Create the hosts mappings.
	slices.SortFunc(peers, func(a, b Peer) int {
		if a.Heartbeat > b.Heartbeat {
			return 1
		} else if a.Heartbeat < b.Heartbeat {
			return -1
		} else {
			return 0
		}
	})
	mappings := hosts.Mapping{
		"pm3": "127.0.0.1",
	}
	for _, p := range peers {
		machineDomain := p.MachineID + ".pm3"
		hostDomain := p.Host + ".pm3"
		ip := p.IP
		if p.Me {
			ip = "127.0.0.1"
		}
		mappings[machineDomain] = ip
		mappings[hostDomain] = ip
	}
	if err := hosts.Insert(mappings); err != nil {
		xlog.WarnC(ctx).Err(err).Msg("Failed to add hosts mappings")
	}

	// Now sort by distance to self, place dead peers at the end
	slices.SortFunc(peers, func(a, b Peer) int {
		aalive := a.Alive()
		balive := b.Alive()
		if aalive && !balive {
			return -1
		} else if !aalive && balive {
			return 1
		} else if a.Distance > b.Distance {
			return 1
		} else if a.Distance < b.Distance {
			return -1
		} else {
			return 0
		}
	})
	return peers, nil
}
func (m *Peerlist) tick(ctx context.Context, self Peer) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		if ctx.Err() != nil {
			return
		}
		updatectx, cancel := context.WithTimeout(ctx, HeartbeatInterval)
		list, err := m.update(updatectx, self)
		if err != nil {
			xlog.WarnC(updatectx).Err(err).Msg("Failed to update peer list")
		}
		cancel()
		if ctx.Err() != nil {
			return
		}

		m.mu.Lock()
		if err == nil {
			m.last = list
			m.errn = 0
		} else {
			m.errn++
			if m.errn > 3 {
				m.err = err
			}
		}
		m.mu.Unlock()
	}
}

func (m *Peerlist) Open(ctx context.Context) error {
	self := FillPeerForSelf(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.self = self
	m.last, m.err = m.update(ctx, self)
	if m.err == nil {
		ctx, m.cancel = context.WithCancel(context.Background())
		go m.tick(ctx, self)
	}
	return m.err
}
func (m *Peerlist) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	mid := m.self.MachineID
	m.mu.Unlock()
	return m.gw.PeerKV.Purge(ctx, mid)
}

// Returns the last error encountered
func (m *Peerlist) Err() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.err
}

// Returns a list of peers, optionally filtering by alive status
func (m *Peerlist) List(alive bool) (r []Peer) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	threshold := time.Now().Add(-HeartbeatTimeout).UnixMilli()

	for i, e := range m.last {
		if !alive || e.Heartbeat > threshold {
			r = append(r, m.last[i])
		}
	}

	// If we're not filtering for alive and there's no alive peers, return self
	if !alive && len(r) == 0 {
		r = append(r, m.self)
	}
	return
}

// Finds a peer by machine ID or hostname
func (m *Peerlist) Find(identifier string) *Peer {
	if identifier == m.self.Host || identifier == m.self.MachineID {
		return &m.self
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.last {
		if m.last[i].MachineID == identifier || m.last[i].Host == identifier {
			return &m.last[i]
		}
	}
	return nil
}
