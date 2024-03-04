package session

import (
	"cmp"
	"context"
	"net/http"
	"sync"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xpost"

	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type SystemMetrics struct {
	MachineID            string             `json:"machine_id"`
	CPU                  cpu.InfoStat       `json:"cpu"`
	Load                 float64            `json:"load"`
	Rx                   float64            `json:"rx"`
	Tx                   float64            `json:"tx"`
	FreeDisk             uint64             `json:"freedisk"`
	FreeMem              uint64             `json:"freemem"`
	TotalDisk            uint64             `json:"totaldisk"`
	TotalMem             uint64             `json:"totalmem"`
	Uptime               uint64             `json:"uptime"` // seconds
	Hostname             string             `json:"hostname"`
	UID                  string             `json:"uid"`
	ProcessCount         uint64             `json:"process_count"`
	OS                   string             `json:"os"`
	KernelVersion        string             `json:"kernel_version"`
	KernelArch           string             `json:"kernel_arch"`
	VirtualizationSystem string             `json:"virtualization_system"`
	VirtualizationRole   string             `json:"virtualization_role"`
	RTT                  map[string]float64 `json:"rtt"`
}
type SessionMetrics struct {
	NumClients int                            `json:"num_clients"`
	Clients    map[string]vhttp.ClientMetrics `json:"sessions"`
}

func GetSystemMetrics(session *Session) (m SystemMetrics) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pendingRtts := lo.Async(func() map[string]float64 {
		return xpost.MeasureConnectivity(ctx, session.Peerlist.List(true))
	})
	defer func() {
		cancel()
		m.RTT = <-pendingRtts
	}()

	m.MachineID = config.GetMachineID().String()
	if v, e := mem.VirtualMemory(); e == nil {
		m.TotalMem = v.Total
		m.FreeMem = v.Free
	}
	if c, e := cpu.Info(); e == nil && len(c) > 0 {
		m.CPU = c[0]
		for i := 1; i < len(c); i++ {
			m.CPU.Cores += c[i].Cores
			m.CPU.CoreID = cmp.Or(m.CPU.CoreID, c[i].CoreID)
			m.CPU.ModelName = cmp.Or(m.CPU.ModelName, c[i].ModelName)
			m.CPU.Mhz = cmp.Or(m.CPU.Mhz, c[i].Mhz)
			m.CPU.PhysicalID = cmp.Or(m.CPU.PhysicalID, c[i].PhysicalID)
			m.CPU.Flags = append(m.CPU.Flags, c[i].Flags...)
			m.CPU.VendorID = cmp.Or(m.CPU.VendorID, c[i].VendorID)
			m.CPU.Family = cmp.Or(m.CPU.Family, c[i].Family)
			m.CPU.Model = cmp.Or(m.CPU.Model, c[i].Model)
			m.CPU.Stepping = cmp.Or(m.CPU.Stepping, c[i].Stepping)
			m.CPU.Microcode = cmp.Or(m.CPU.Microcode, c[i].Microcode)
		}
	}
	t0 := time.Now()
	n, _ := net.IOCounters(false)
	if l, e := cpu.Percent(time.Second, false); e == nil {
		m.Load = l[0]
	}
	n2, _ := net.IOCounters(false)
	if n != nil && n2 != nil {
		tdelta := float64(time.Since(t0)) / float64(time.Second)
		for _, v := range n2 {
			m.Rx += float64(v.BytesRecv)
			m.Tx += float64(v.BytesSent)
		}
		for _, v := range n {
			m.Rx -= float64(v.BytesRecv)
			m.Tx -= float64(v.BytesSent)
		}
		m.Rx /= tdelta
		m.Tx /= tdelta
	}
	if d, e := disk.Usage("."); e == nil {
		m.FreeDisk = d.Free
		m.TotalDisk = d.Total
	}
	if u, e := host.Uptime(); e == nil {
		m.Uptime = u
	}
	if o, e := host.Info(); e == nil {
		m.Hostname = o.Hostname
		m.UID = o.HostID
		m.ProcessCount = o.Procs
		m.OS = o.OS
		m.KernelVersion = o.KernelVersion
		m.KernelArch = o.KernelArch
		m.VirtualizationSystem = o.VirtualizationSystem
		m.VirtualizationRole = o.VirtualizationRole
	}
	return
}

var systemMetricsCache SystemMetrics
var systemMetricsCacheTime time.Time
var systemMetricsCacheLock = sync.RWMutex{}

func init() {
	Match("/system", func(s *Session, r *http.Request, _ struct{}) (SystemMetrics, error) {
		systemMetricsCacheLock.RLock()
		if time.Since(systemMetricsCacheTime) < time.Second {
			m := systemMetricsCache
			systemMetricsCacheLock.RUnlock()
			return m, nil
		}
		systemMetricsCacheLock.RUnlock()
		systemMetricsCacheLock.Lock()
		defer systemMetricsCacheLock.Unlock()
		if time.Since(systemMetricsCacheTime) < time.Second {
			m := systemMetricsCache
			return m, nil
		}
		m := GetSystemMetrics(s)
		systemMetricsCache = m
		systemMetricsCacheTime = time.Now()
		return m, nil
	})
	Match("/session", func(session *Session, r *http.Request, _ struct{}) (m SessionMetrics, _ error) {
		m.NumClients = vhttp.NumClients()
		m.Clients = vhttp.GetClientMetrics()
		return
	})
}
