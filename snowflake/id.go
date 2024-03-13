package snowflake

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/util"
	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/host"
)

const (
	EpochBegin     = int64(1704067200000) // 2024-01-01T00:00:00Z
	SeqShift       = 0
	SeqMask        = uint64(0xFFF)
	MachineIDShift = 12
	MachineIDMask  = uint64(0x3FF000)
	TimestampShift = 22
)

type ID uint64

type Generator struct {
	MachineID uint32
	Sequence  uint32
}

func (g *Generator) NextAtUnixMilli(t int64) ID {
	return FromParts(g.MachineID, atomic.AddUint32(&g.Sequence, 1), t)
}
func (g *Generator) NextAt(t time.Time) ID {
	return g.NextAtUnixMilli(t.UnixMilli())
}
func (g *Generator) Next() ID {
	return g.NextAt(time.Now())
}

var DefaultGenerator = &Generator{}

func init() {
	var buf [8]byte
	lo.Must(rand.Read(buf[:]))
	DefaultGenerator.Sequence = binary.LittleEndian.Uint32(buf[4:])

	i, _ := host.Info()
	hash := sha1.Sum([]byte(i.HostID + i.Hostname))
	DefaultGenerator.MachineID = binary.BigEndian.Uint32(hash[:4])
}

func New() ID {
	return DefaultGenerator.Next()
}
func Zero() ID {
	return ID(0)
}

func FromParts(machineID uint32, seq uint32, unixMilli int64) ID {
	v := uint64(machineID) & uint64(MachineIDMask)
	v |= uint64(seq) & SeqMask
	v |= uint64(unixMilli-EpochBegin) << TimestampShift
	return ID(v)
}

func From(value any) ID {
	switch v := value.(type) {
	case string:
		val, _ := strconv.ParseUint(v, 10, 64)
		return ID(val)
	case int64:
		return ID(v)
	case uint64:
		return ID(v)
	case time.Time:
		return DefaultGenerator.NextAt(v)
	default:
		return 0
	}
}

func (i ID) String() string {
	return strconv.FormatUint(uint64(i), 10)
}
func (i ID) Valid() bool {
	return 0 < uint64(i) && uint64(i) < 0xffffffffffffffff
}
func (i ID) IsZero() bool {
	return i == 0
}
func (i ID) Lowerbound() ID {
	return ID(uint64(i) & ^(SeqMask | MachineIDMask))
}
func (i ID) Upperbound() ID {
	return ID(uint64(i) | SeqMask | MachineIDMask)
}
func (i ID) Sequence() uint32 {
	return uint32(i) & uint32(SeqMask)
}
func (i ID) MachineID() uint32 {
	return (uint32(i) & uint32(MachineIDMask)) >> MachineIDShift
}
func (i ID) Timestamp() time.Time {
	return time.UnixMilli(int64((uint64(i) >> TimestampShift) + uint64(EpochBegin)))
}
func (i ID) MarshalText() (res []byte, e error) {
	res = make([]byte, 0, 20)
	res = strconv.AppendUint(res, uint64(i), 10)
	return res, nil
}
func (i *ID) UnmarshalText(data []byte) error {
	val, err := strconv.ParseUint(util.UnsafeString(data), 10, 64)
	if err != nil {
		return err
	}
	*i = ID(val)
	return nil
}
func (i ID) ToSlice() []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(i))
	return buf[:]
}
func (i ID) MarshalJSON() (res []byte, e error) {
	if i == 0 {
		return []byte("null"), nil
	}
	res = make([]byte, 1, 20)
	res[0] = '"'
	res = strconv.AppendUint(res, uint64(i), 10)
	res = append(res, '"')
	return res, nil
}

func (i *ID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] == 'n' {
		*i = 0
		return nil
	}
	if data[0] != '"' {
		var ui uint64
		err := json.Unmarshal(data, &ui)
		*i = ID(ui)
		return err
	} else {
		return i.UnmarshalText(data[1 : len(data)-1])
	}
}
