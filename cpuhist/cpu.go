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
	n, _ := cpu.Counts(true)
	if n != 0 {
		NumCPU = n
	}
}

type processLike interface {
	times() (*cpu.TimesStat, error)
	alive() bool
	creation() time.Time
	pid() int32
}
type realProcess struct {
	proc *process.Process
}

func (p realProcess) pid() int32                     { return p.proc.Pid }
func (p realProcess) times() (*cpu.TimesStat, error) { return p.proc.Times() }
func (p realProcess) alive() (running bool)          { running, _ = p.proc.IsRunning(); return }
func (p realProcess) creation() time.Time            { ts, _ := p.proc.CreateTime(); return time.UnixMilli(ts) }

type systemProcess struct{}

var bootTime, _ = host.BootTime()

func (p systemProcess) pid() int32 { return -1 }
func (p systemProcess) times() (*cpu.TimesStat, error) {
	res, err := cpu.Times(false)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(res); i++ {
		res[0].User += res[i].User
		res[0].System += res[i].System
		res[0].Nice += res[i].Nice
		res[0].Iowait += res[i].Iowait
		res[0].Irq += res[i].Irq
		res[0].Softirq += res[i].Softirq
		res[0].Steal += res[i].Steal
		res[0].Guest += res[i].Guest
		res[0].GuestNice += res[i].GuestNice
		res[0].Idle += res[i].Idle
	}
	return &res[0], nil
}
func (p systemProcess) alive() bool         { return true }
func (p systemProcess) creation() time.Time { return time.Unix(int64(bootTime), 0) }

type cpuUsageRecord struct {
	cpuSeconds float64
	timestamp  time.Time
}

func newCpuUsageRecord(p processLike) (r cpuUsageRecord) {
	if cpu, err := p.times(); err == nil {
		r.cpuSeconds = cpu.System + cpu.User + cpu.Irq
	}
	r.timestamp = time.Now()
	return
}
func (from cpuUsageRecord) getAverageUseBetween(to cpuUsageRecord) float64 {
	cpuSecElapsed := to.timestamp.Sub(from.timestamp).Seconds()
	cpuSecUsed := (to.cpuSeconds - from.cpuSeconds)
	usePercent := cpuSecUsed / cpuSecElapsed
	usePercent = min(1, max(usePercent, 0))
	return usePercent * float64(NumCPU)
}

type cpuUsageHistory struct {
	lastUse atomic.Int64
	mu      sync.Mutex
	proc    processLike
	r0, r1  cpuUsageRecord
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

	record := newCpuUsageRecord(h.proc)

	h.lastUse.Store(record.timestamp.UnixNano())

	if h.r1.timestamp.Before(record.timestamp.Add(-cpuUsageInterval)) {
		if h.r1.timestamp.IsZero() {
			h.r0.timestamp = h.proc.creation()
		} else {
			h.r0 = h.r1
		}
		h.r1 = record
	}
	return h.r0.getAverageUseBetween(record)
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
