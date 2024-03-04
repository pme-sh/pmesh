package ray

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"get.pme.sh/pmesh/snowflake"
	"get.pme.sh/pmesh/util"
)

type ID struct {
	ID   snowflake.ID
	Host Host
}

var ErrInvalidRayID = errors.New("invalid ray ID")

const RayLength = 16 + 1 + 3

func Parse(s string) (r ID, e error) {
	e = r.UnmarshalText(util.UnsafeBuffer(s))
	return
}

func (r ID) Timestamp() time.Time {
	return r.ID.Timestamp()
}
func (r ID) String() string {
	arr := r.ToArray()
	return string(arr[:])
}
func (r ID) ToArray() (text [RayLength]byte) {
	var buf [RayLength]byte

	var sno [8]byte
	binary.BigEndian.PutUint64(sno[:], uint64(r.ID))
	hex.Encode(buf[:16], sno[:])

	buf[16] = '-'
	buf[17] = r.Host[0]
	buf[18] = r.Host[1]
	buf[19] = r.Host[2]
	return buf
}
func (r ID) MarshalText() (text []byte, err error) {
	arr := r.ToArray()
	return arr[:], nil
}
func (r *ID) UnmarshalText(text []byte) error {
	if len(text) != RayLength || text[16] != '-' {
		return ErrInvalidRayID
	}
	var sno [8]byte
	if _, err := hex.Decode(sno[:], text[:16]); err != nil {
		return ErrInvalidRayID
	}
	r.ID = snowflake.ID(binary.BigEndian.Uint64(sno[:]))
	r.Host[0] = text[17]
	r.Host[1] = text[18]
	r.Host[2] = text[19]
	r.Host = r.Host.Normal()
	return nil
}

type Generator struct {
	prefmt [RayLength]byte
}

func NewGenerator(host string) (r Generator) {
	ray := ID{ID: 0, Host: ParseHost(host)}
	rayb, _ := ray.MarshalText()
	copy(r.prefmt[:], rayb)
	return
}
func (r *Generator) Next() string {
	buf := r.prefmt
	var sno [8]byte
	binary.BigEndian.PutUint64(sno[:], uint64(snowflake.New()))
	hex.Encode(buf[:16], sno[:])
	return string(buf[:])
}
