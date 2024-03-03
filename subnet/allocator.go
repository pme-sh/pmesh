package subnet

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
)

type Allocator struct {
	netip    net.IPNet
	step     atomic.Uint32
	paranoid bool
	mu       sync.Mutex
	taken    map[uint32]struct{}
	bound    map[uint32]struct{}
}

func NewAllocator(cidr string, paranoid bool) *Allocator {
	_, netip, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Fatalf("Failed to initialize subnet %q: %v", cidr, err)
	}
	if len(netip.IP) != 4 {
		log.Fatalf("Failed to initialize subnet %q: Invalid IPv4", cidr)
	}

	allocator := &Allocator{
		netip:    *netip,
		taken:    make(map[uint32]struct{}),
		bound:    make(map[uint32]struct{}),
		paranoid: paranoid || runtime.GOOS == "darwin",
	}
	allocator.step.Store(1)
	return allocator
}
func ipToU32(ip net.IP) uint32 {
	a := ip
	if len(a) != 4 {
		a = a[len(a)-4:]
	}
	return binary.LittleEndian.Uint32(a)
}

func (a *Allocator) Deallocate(ip net.IP) {
	a.mu.Lock()
	delete(a.taken, ipToU32(ip))
	a.mu.Unlock()
}
func (a *Allocator) tryBindAlias(ip net.IP) {
	if runtime.GOOS == "darwin" {
		exec.Command("sudo", "ifconfig", "lo0", "alias", ip.String(), "up").Run()
	}
}
func (a *Allocator) Generate() (ip net.IP) {
	next := a.step.Add(1)
	r := [4]byte{
		byte(next >> 24),
		byte(next >> 16),
		byte(next >> 8),
		byte(next),
	}

	// Apply the mask
	for j := 0; j != 4; j++ {
		r[j] &= ^a.netip.Mask[j]
		r[j] |= a.netip.IP[j]
	}
	return net.IP(r[:])
}
func (a *Allocator) Allocate(port int) (ip net.IP, err error) {
	var lastErr error
	for i := 0; i != 64; i++ {
		ip = a.Generate()
		r := ipToU32(ip)

		// Check if it's taken
		a.mu.Lock()
		_, taken := a.taken[r]
		if !taken {
			a.taken[r] = struct{}{}
		}
		_, bound := a.bound[r]
		if !bound {
			a.bound[r] = struct{}{}
		}
		a.mu.Unlock()

		// If first time binding, try to bind.
		if !bound {
			a.tryBindAlias(ip)
		}

		if !taken {
			// Confirm
			addr := &net.TCPAddr{
				IP:   ip,
				Port: port,
			}
			if a.paranoid {
				var listener net.Listener
				listener, err = net.ListenTCP("tcp4", addr)
				if err != nil {
					lastErr = err
					continue
				}
				listener.Close()
			}
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no available addresses in %s (%w)", a.netip.String(), lastErr)
}
func (a *Allocator) AllocateContext(ctx context.Context, port int) (ip net.IP, err error) {
	ip, err = a.Allocate(port)
	if err == nil {
		context.AfterFunc(ctx, func() {
			a.Deallocate(ip)
		})
	}
	return
}
