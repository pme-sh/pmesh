package snowflake

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/util"

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

func (value ID) String() string {
	return strconv.FormatUint(uint64(value), 10)
}
func (value ID) Valid() bool {
	return 0 < uint64(value) && uint64(value) < 0xffffffffffffffff
}
func (value ID) IsZero() bool {
	return value == 0
}
func (value ID) Lowerbound() ID {
	return ID(uint64(value) & ^SeqMask)
}
func (value ID) Upperbound() ID {
	return ID(uint64(value) | SeqMask)
}
func (value ID) Sequence() uint32 {
	return uint32(value) & uint32(SeqMask)
}
func (value ID) MachineID() uint32 {
	return (uint32(value) & uint32(MachineIDMask)) >> MachineIDShift
}
func (value ID) Timestamp() time.Time {
	return time.UnixMilli(int64((uint64(value) >> TimestampShift) + uint64(EpochBegin)))
}
func (value ID) MarshalText() (b []byte, e error) {
	b = strconv.AppendUint(b, uint64(value), 10)
	return
}
func (value *ID) UnmarshalText(data []byte) error {
	str := util.UnsafeString(data)
	val, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return err
	}
	*value = ID(val)
	return nil
}
func (value ID) ToSlice() []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(value))
	return buf[:]
}
func (value ID) MarshalJSON() ([]byte, error) {
	if value == 0 {
		return []byte("null"), nil
	}
	return []byte(fmt.Sprintf(`"%d"`, uint64(value))), nil
}

func (value *ID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] == 'n' {
		*value = 0
		return nil
	}
	if data[0] != '"' {
		var ui uint64
		err := json.Unmarshal(data, &ui)
		*value = ID(ui)
		return err
	} else {
		return value.UnmarshalText(data[1 : len(data)-1])
	}
}
