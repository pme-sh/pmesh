package netx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"get.pme.sh/pmesh/util"
)

// Well-known addresses
var (
	IPv4zero             = IPv4(0, 0, 0, 0) // all zeros
	IPv4StandardLoopback = IPv4(127, 0, 0, 1)
	IPv6loopback         = FromIP(net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
)

func parseUint(b []byte) (r uint, ok bool) {
	for _, c := range b {
		if c < '0' {
			return
		}
		d := c - '0'
		if d > 9 {
			return
		}
		r = r*10 + uint(d)
	}
	return r, true
}
func parseUint8(b []byte) (uint8, bool) {
	r, ok := parseUint(b)
	return uint8(r), ok && r <= 255
}
func parseUint16(b []byte) (uint16, bool) {
	r, ok := parseUint(b)
	return uint16(r), ok && r <= 65535
}

type IP struct {
	Low  uint64
	High uint64
}

func IPv4Uint32(a uint32) IP {
	return IP{Low: 0x0000_ffff_0000_0000 | uint64(a)}
}
func IPv4(a, b, c, d byte) IP {
	lo := 0x0000_ffff_0000_0000 | uint64(a)<<24 | uint64(b)<<16 | uint64(c)<<8 | uint64(d)
	return IP{Low: lo}
}
func IPv6(a, b, c, d, e, f, g, h uint8) IP {
	return IP{
		Low:  uint64(e) | uint64(d)<<16 | uint64(c)<<32 | uint64(b)<<48,
		High: uint64(h) | uint64(g)<<16 | uint64(f)<<32 | uint64(a)<<48,
	}
}

func ParseIP(ip string) (s IP) {
	addr, err := netip.ParseAddr(ip)
	if err == nil {
		s = IPFromAddr(addr)
	}
	return
}
func IPFromAddr(a netip.Addr) IP {
	return IPFromSlice(a.AsSlice())
}
func IPFromSlice(a []byte) (i IP) {
	if len(a) == 4 {
		return IPv4(a[0], a[1], a[2], a[3])
	}
	if len(a) == 16 {
		i.Low = binary.BigEndian.Uint64(a[8:])
		i.High = binary.BigEndian.Uint64(a[:8])
	}
	return
}
func FromIP(ip net.IP) IP {
	return IPFromSlice(ip)
}
func IPFromMask(ones uint8) (r IP) {
	switch {
	case ones >= 128:
		r.Low = 0xffff_ffff_ffff_ffff
		r.High = 0xffff_ffff_ffff_ffff
	case ones >= 64:
		r.Low = 0xffff_ffff_ffff_ffff
		ones &= 63
		r.High = (1 << ones) - 1
	default:
		r.Low = (1 << ones) - 1
	}
	return
}

func (i IP) Shr(n uint8) IP {
	res := i
	if n >= 64 {
		res.Low = res.High
		res.High = 0
		n &= 63
	}
	carry := res.High << (64 - n)
	res.High >>= n
	res.Low = (res.Low >> n) | carry
	return res
}
func (i IP) BitOr(o IP) IP {
	return IP{i.Low | o.Low, i.High | o.High}
}

func (i *IP) UnmarshalText(b []byte) error {
	adr, e := netip.ParseAddr(util.UnsafeString(b))
	if e != nil {
		return errors.New("invalid ip")
	}
	*i = IPFromAddr(adr)
	return nil
}
func (i IP) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}
func (i IP) IsZero() bool {
	return i.Low == 0 && i.High == 0
}
func (i IP) IsV4() bool {
	return i.High == 0 && ((i.Low>>32) == 0xffff || i.Low == 0)
}
func (i IP) String() string {
	return i.ToAddr().String()
}
func (i IP) ToSlice() []byte {
	if i.IsV4() {
		if i.Low == 0 {
			return []byte{0, 0, 0, 0}
		}
		return []byte{
			byte(i.Low >> 24),
			byte(i.Low >> 16),
			byte(i.Low >> 8),
			byte(i.Low),
		}
	} else {
		var a [16]byte
		binary.BigEndian.PutUint64(a[8:], i.Low)
		binary.BigEndian.PutUint64(a[:8], i.High)
		return a[:]
	}
}
func (i IP) ToAddr() netip.Addr {
	if i.IsV4() {
		return netip.AddrFrom4([4]byte(i.ToSlice()))
	} else {
		return netip.AddrFrom16([16]byte(i.ToSlice()))
	}
}
func (i IP) ToIP() net.IP {
	return net.IP(i.ToSlice())
}
func (s IP) Compare(o IP) int64 {
	if s.High != o.High {
		return int64(s.High - o.High)
	} else {
		return int64(s.Low - o.Low)
	}
}
func (s IP) Equal(o IP) bool {
	return s == o
}
func (ip IP) IsUnspecified() bool {
	return ip.Equal(IPv4zero) || ip.IsZero()
}

// IsLoopback reports whether ip is a loopback address.
func (ip IP) IsLoopback() bool {
	if ip.IsV4() {
		return uint8(ip.Low>>24) == 127
	}
	return ip.Equal(IPv6loopback)
}
func (ip IP) IsPublic() bool {
	return !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsPrivate()
}

// IsPrivate reports whether ip is a private address, according to
// RFC 1918 (IPv4 addresses) and RFC 4193 (IPv6 addresses).
func (ip IP) IsPrivate() bool {
	if ip.IsV4() {
		ip0 := uint8(ip.Low >> 24)
		ip1 := uint8(ip.Low >> 16)
		return ip0 == 10 ||
			(ip0 == 172 && ip1&0xf0 == 16) ||
			(ip0 == 192 && ip1 == 168)
	}
	ip0 := uint8(ip.High >> 56)
	return ip0&0xfe == 0xfc
}

type IPPort struct {
	IP   IP
	Port uint16
}

func ParseIPPort(ip string) (s IPPort) {
	s.UnmarshalText(util.UnsafeBuffer(ip))
	return
}
func FromAddrPort(a netip.AddrPort) IPPort {
	return IPPort{
		IP:   IPFromAddr(a.Addr()),
		Port: a.Port(),
	}
}
func (i IPPort) ToAddrPort() netip.AddrPort {
	return netip.AddrPortFrom(i.IP.ToAddr(), i.Port)
}
func (i IPPort) String() string {
	if i.IP.IsV4() {
		return fmt.Sprintf("%s:%d", i.IP.String(), i.Port)
	} else {
		return fmt.Sprintf("[%s]:%d", i.IP.String(), i.Port)
	}
}
func (i IPPort) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}
func (i *IPPort) UnmarshalText(b []byte) error {
	idx := bytes.LastIndexByte(b, ':')
	if idx <= 0 {
		return errors.New("missing : in ip:port")
	}
	var ok bool
	i.Port, ok = parseUint16(b[idx+1:])
	if !ok {
		return fmt.Errorf("invalid port: %s", b[idx+1:])
	}
	if b[0] == '[' {
		b = b[1 : idx-1]
	} else {
		b = b[:idx]
	}
	return i.IP.UnmarshalText(b)
}

type IPNet struct {
	IP    IP
	Shift uint8
}

func ParseIPNet(ip string) (s IPNet) {
	s.UnmarshalText(util.UnsafeBuffer(ip))
	return
}
func FromIPNet(a *net.IPNet) IPNet {
	ones, _ := a.Mask.Size()
	if v4 := a.IP.To4(); v4 != nil {
		return IPNet{
			IP:    FromIP(v4),
			Shift: uint8(32 - ones),
		}
	} else {
		return IPNet{
			IP:    FromIP(a.IP),
			Shift: uint8(128 - ones),
		}
	}
}
func (i IPNet) ToIPNet() *net.IPNet {
	return &net.IPNet{
		IP:   i.IP.ToIP(),
		Mask: net.CIDRMask(i.Size()),
	}
}
func (i IPNet) Size() (ones, bits int) {
	if i.IP.IsV4() {
		return 32 - int(i.Shift), 32
	} else {
		return 128 - int(i.Shift), 128
	}
}
func (i IPNet) Limit() IP {
	return i.IP.BitOr(IPFromMask(i.Shift))
}
func (i IPNet) Contains(o IP) bool {
	return i.IP.Shr(i.Shift) == o.Shr(i.Shift)
}
func (i IPNet) String() string {
	ones, _ := i.Size()
	return i.IP.String() + "/" + strconv.Itoa(ones)
}
func (i IPNet) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}
func (i *IPNet) UnmarshalText(b []byte) (e error) {
	idx := bytes.IndexByte(b, '/')
	if idx < 0 {
		return errors.New("invalid CIDR")
	}
	e = i.IP.UnmarshalText(b[:idx])
	if e == nil {
		v, ok := parseUint8(b[idx+1:])
		if !ok {
			return errors.New("invalid CIDR")
		}
		if i.IP.IsV4() {
			i.Shift = uint8(32 - v)
		} else {
			i.Shift = uint8(128 - v)
		}
	}
	return
}

func FromNetAddr(adr net.Addr) IPPort {
	if addr, ok := adr.(*net.TCPAddr); ok {
		return IPPort{FromIP(addr.IP), uint16(addr.Port)}
	} else if addr, ok := adr.(*net.UDPAddr); ok {
		return IPPort{FromIP(addr.IP), uint16(addr.Port)}
	} else if adrp := ParseIPPort(adr.String()); !adrp.IP.IsZero() {
		return adrp
	} else {
		host, p, err := net.SplitHostPort(adr.String())
		if err != nil {
			return IPPort{}
		}
		ipr, err := net.ResolveIPAddr("ip", host)
		if err != nil {
			return IPPort{}
		}
		port, ok := parseUint16(util.UnsafeBuffer(p))
		if !ok {
			return IPPort{}
		}
		return IPPort{FromIP(ipr.IP), uint16(port)}
	}
}
func GetOutboundIP() (res IP) {
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return
	}
	defer conn.Close()
	localAddr := conn.LocalAddr()
	return FromNetAddr(localAddr).IP
}

type ListenerInfo struct {
	LocalAddr    IPPort
	OutboundAddr IPPort
}

func QueryListener(lst net.Listener) (info ListenerInfo) {
	info.LocalAddr = FromNetAddr(lst.Addr())
	info.OutboundAddr = info.LocalAddr
	if info.OutboundAddr.IP.IsUnspecified() {
		info.OutboundAddr = IPPort{GetOutboundIP(), info.OutboundAddr.Port}
		info.LocalAddr.IP = IPv4StandardLoopback
	}
	return
}
