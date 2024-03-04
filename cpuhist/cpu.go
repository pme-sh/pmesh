package cpuhist

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/concurrent"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"
)

const cpuUsageInterval = 5 * time.Second

var NumCPU = runtime.NumCPU()

func init() {
	n, _ := cpu.Counts(false)
	if n != 0 {
		NumCPU = n
	}
}

type times struct {
	used      float64
	idle      float64
	timestamp time.Time
	isprocess bool
}

func (from times) getAverageUseBetween(to times) (float64, bool) {
	if from.timestamp.IsZero() || to.timestamp.IsZero() {
		return 0, false
	}

	used := to.used - from.used
	if used < 0 {
		return 0, false
	}

	if !to.isprocess {
		idle := to.idle - from.idle
		if idle < 0 {
			return 0, false
		}
		elapsed := used + idle
		p := used / elapsed
		p = min(float64(1), max(p, 0.0))
		return p, true
	} else {
		elapsed := to.timestamp.Sub(from.timestamp).Seconds()
		p := used / elapsed
		p = min(float64(NumCPU), max(p, 0.0))
		return p, true
	}
}

type processLike interface {
	times() times
	alive() bool
	creation() time.Time
	pid() int32
}
type realProcess struct {
	proc *process.Process
}

func (p realProcess) pid() int32            { return p.proc.Pid }
func (p realProcess) alive() (running bool) { running, _ = p.proc.IsRunning(); return }
func (p realProcess) creation() time.Time   { ts, _ := p.proc.CreateTime(); return time.UnixMilli(ts) }
func (p realProcess) times() (r times) {
	u, err := p.proc.Times()
	if err != nil {
		return
	}
	r.used = u.User + u.System + u.Iowait + u.Irq + u.Nice + u.Softirq + u.Steal + u.Guest + u.GuestNice
	r.idle = 0
	r.timestamp = time.Now()
	r.isprocess = true
	return
}

type systemProcess struct{}

var bootTime, _ = host.BootTime()

func (p systemProcess) pid() int32 { return -1 }
func (p systemProcess) times() (r times) {
	res, err := cpu.Times(false)
	if err != nil {
		return
	}
	for _, v := range res {
		r.used += v.User + v.System + v.Iowait + v.Irq + v.Nice + v.Softirq + v.Steal + v.Guest + v.GuestNice
		r.idle += v.Idle
	}
	r.timestamp = time.Now()
	r.isprocess = false
	return
}
func (p systemProcess) alive() bool         { return true }
func (p systemProcess) creation() time.Time { return time.Unix(int64(bootTime), 0) }

type cpuUsageHistory struct {
	lastUse  atomic.Int64
	mu       sync.Mutex
	proc     processLike
	r0, r1   times
	lastobsv float64
}
type uniqueProcessID struct {
	createtime int64
	pid        int32
}

func (h *cpuUsageHistory) abandoned() bool {
	if !h.proc.alive() {
		return true
	}
	if h.lastUse.Load() < time.Now().Add(-time.Minute).UnixNano() {
		return true
	}
	return false
}
func (h *cpuUsageHistory) getAverageUse() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.proc.times()
	if record.timestamp.IsZero() {
		return 0
	}

	h.lastUse.Store(record.timestamp.UnixNano())

	if h.r1.timestamp.Before(record.timestamp.Add(-cpuUsageInterval)) {
		if h.r1.timestamp.IsZero() {
			h.r0.timestamp = h.proc.creation()
		} else {
			h.r0 = h.r1
		}
		h.r1 = record
	}
	res, ok := h.r0.getAverageUseBetween(record)
	if !ok {
		return h.lastobsv
	}
	h.lastobsv = res
	return res
}

var cpuUsageHistories = concurrent.Map[uniqueProcessID, *cpuUsageHistory]{}
var cpuUsageHistoryCleanup = sync.Once{}

func getUsePercentage(proc processLike) float64 {
	cpuUsageHistoryCleanup.Do(func() {
		go func() {
			for range time.Tick(time.Minute) {
				cpuUsageHistories.Range(func(k uniqueProcessID, v *cpuUsageHistory) bool {
					if v.abandoned() {
						cpuUsageHistories.Delete(k)
					}
					return true
				})
			}
		}()
	})

	if !proc.alive() {
		return 0
	}

	uid := uniqueProcessID{pid: proc.pid(), createtime: proc.creation().UnixNano()}
	history, ok := cpuUsageHistories.Load(uid)
	if !ok {
		history, _ = cpuUsageHistories.LoadOrStore(uid, &cpuUsageHistory{proc: proc})
	}
	return history.getAverageUse()
}

// GetUsePercentage returns the average CPU usage percentage of the process.
// Percentage is expressed as [0, #CPU] where 0 means no usage and #CPU means full usage of all cores.
// The average is calculated over a period of time.
func GetUsePercentage(proc *process.Process) float64 {
	return getUsePercentage(realProcess{proc})
}

// GetSystemUsePercentage returns the average CPU usage percentage of the system.
// Percentage is expressed as [0, 1] where 0 means no usage and 1 means full usage of all cores.
// The average is calculated over a period of time.
func GetSystemUsePercentage() float64 {
	return getUsePercentage(systemProcess{})
}
