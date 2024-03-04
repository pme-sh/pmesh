package service

import (
	"strings"
	"time"

	"get.pme.sh/pmesh/cpuhist"

	"github.com/google/shlex"
	"github.com/shirou/gopsutil/v3/process"
)

type ProcessTree struct {
	Parent *process.Process
	Tree   map[int32]*process.Process
}

func (tree ProcessTree) IsZero() bool {
	return len(tree.Tree) == 0
}
func (tree ProcessTree) Refresh() {
	tree.Tree = nil
	tree.AddProcess(tree.Parent)
}
func (tree *ProcessTree) AddProcess(p *process.Process) {
	if p == nil || p.Pid == 0 {
		return
	}
	if tree.Tree == nil {
		tree.Parent = p
		tree.Tree = map[int32]*process.Process{p.Pid: p}
	} else {
		if _, ok := tree.Tree[p.Pid]; ok {
			return
		}
		tree.Tree[p.Pid] = p
	}
	if children, err := p.Children(); err == nil {
		for _, child := range children {
			tree.AddProcess(child)
		}
	}
}
func (tree *ProcessTree) AddProcessPid(pid int32) {
	p, _ := process.NewProcess(pid)
	tree.AddProcess(p)
}
func (tree ProcessTree) IsParentRunning() bool {
	if tree.Parent == nil {
		return false
	}
	running, _ := tree.Parent.IsRunning()
	return running
}
func (tree ProcessTree) Kill() {
	for _, p := range tree.Tree {
		p.Kill()
	}
}
func NewProcessTree(p *process.Process) (tree ProcessTree) {
	tree.AddProcess(p)
	return
}

type ProcUsageMetrics struct {
	CPU     float64 `json:"cpu,omitempty"`
	RSS     uint64  `json:"rss,omitempty"`
	VMS     uint64  `json:"vms,omitempty"`
	HWM     uint64  `json:"hwm,omitempty"`
	Data    uint64  `json:"data,omitempty"`
	Stack   uint64  `json:"stack,omitempty"`
	Locked  uint64  `json:"locked,omitempty"`
	Swap    uint64  `json:"swap,omitempty"`
	IoRead  uint64  `json:"io_read,omitempty"`
	IoWrite uint64  `json:"io_write,omitempty"`
}

func (u *ProcUsageMetrics) Combine(other ProcUsageMetrics) {
	u.CPU += other.CPU
	u.RSS += other.RSS
	u.VMS += other.VMS
	u.HWM += other.HWM
	u.Data += other.Data
	u.Stack += other.Stack
	u.Locked += other.Locked
	u.Swap += other.Swap
	u.IoRead += other.IoRead
	u.IoWrite += other.IoWrite
}
func (u *ProcUsageMetrics) Fill(proc *process.Process) {
	u.CPU = cpuhist.GetUsePercentage(proc)
	if mi, err := proc.MemoryInfo(); err == nil && mi != nil {
		u.RSS = mi.RSS
		u.VMS = mi.VMS
		u.HWM = mi.RSS
		u.Data = mi.Data
		u.Stack = mi.Stack
		u.Locked = mi.Locked
		u.Swap = mi.Swap
	}
	if io, err := proc.IOCounters(); err == nil {
		u.IoRead = io.ReadBytes
		u.IoWrite = io.WriteBytes
	}
}

type ProcMetrics struct {
	PID        int32     `json:"pid"`
	CreateTime time.Time `json:"create_time,omitempty"`
	Cmd        string    `json:"cmd,omitempty"`
	ProcUsageMetrics
}

func (m *ProcMetrics) BriefCmd() string {
	p, err := shlex.Split(m.Cmd)
	if err != nil || len(p) == 0 {
		return m.Cmd
	}

	// Remove all -xxx --xxx options
	s := []string{p[0]}
	skip := false
	for _, part := range p[1:] {
		if skip {
			skip = false
			continue
		}
		if len(part) > 1 && part[0] == '-' {
			hasEqual := strings.Contains(part, "=")
			if !hasEqual {
				skip = true
			}
			continue
		}
		s = append(s, part)
	}
	return strings.Join(s, " ")
}

func GetProcMetrics(p *process.Process) (metrics ProcMetrics) {
	if p == nil {
		return
	}
	metrics.PID = p.Pid
	metrics.ProcUsageMetrics.Fill(p)
	if epo, err := p.CreateTime(); err == nil {
		metrics.CreateTime = time.UnixMilli(epo)
	}
	if cmd, err := p.Cmdline(); err == nil {
		metrics.Cmd = cmd
	}
	return
}

type ProcTreeMetrics struct {
	ProcMetrics
	Tree map[int32]ProcMetrics `json:"tree,omitempty"`
}

func (tree ProcessTree) Metrics() (metrics ProcTreeMetrics) {
	if tree.Parent == nil {
		return
	}

	metrics.ProcMetrics = GetProcMetrics(tree.Parent)
	metrics.Tree = make(map[int32]ProcMetrics, len(tree.Tree))
	metrics.Tree[tree.Parent.Pid] = metrics.ProcMetrics
	for pid, p := range tree.Tree {
		if pid != tree.Parent.Pid {
			m := GetProcMetrics(p)
			metrics.Tree[pid] = m
			metrics.ProcMetrics.ProcUsageMetrics.Combine(m.ProcUsageMetrics)
		}
	}
	return
}
