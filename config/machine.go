package config

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v3/host"
)

type MachineID uint32

func (m MachineID) Uint32() uint32 {
	return uint32(m)
}
func (m MachineID) String() string {
	return fmt.Sprintf("%08x", uint32(m))
}
func (m MachineID) ToSlice() []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(m))
	return b[:]
}

var GetMachineID = sync.OnceValue(func() MachineID {
	hash := sha1.New()
	hn, _ := os.Hostname()
	hash.Write([]byte(hn + "---pmesh"))
	if hid, err := host.HostID(); err == nil && len(hid) > 0 {
		hash.Write([]byte(strings.ToLower(hid)))
	}
	s := hash.Sum(nil)
	s[0] |= 2
	return MachineID(binary.LittleEndian.Uint32(s[:4]))
})
