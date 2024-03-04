package ray

import (
	"fmt"

	"get.pme.sh/pmesh/util"
)

type Host [3]byte

func ParseHost(s string) (p Host) {
	copy(p[:], s)
	return p.Normal()
}
func ToHostString(s string) string {
	return ParseHost(s).String()
}

func (p Host) String() string {
	buf := p.Normal()
	return util.UnsafeString(buf[:])
}
func (p Host) Normal() Host {
	for i, c := range p {
		if c == 0 {
			p[i] = 'X'
		} else if 'a' <= c && c <= 'z' {
			p[i] = c - 'a' + 'A'
		}
	}
	return p
}
func (p *Host) UnmarshalText(text []byte) error {
	if len(text) != 3 {
		return fmt.Errorf("invalid public host: %q", text)
	}
	copy(p[:], text)
	*p = p.Normal()
	return nil
}
func (p Host) MarshalText() ([]byte, error) {
	return p[:], nil
}
func (p Host) EqualFold(s string) bool {
	return p == ParseHost(s)
}
