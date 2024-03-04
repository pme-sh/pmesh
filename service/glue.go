package service

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"get.pme.sh/pmesh/config"

	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/process"
)

const gpsize = 4 + 4 + 8

type gluedProcess struct {
	pid  int32
	ppid int32
	tim  int64
}

func (r gluedProcess) toSlice() (buf []byte) {
	buf = make([]byte, gpsize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(r.pid))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(r.ppid))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(r.tim))
	return
}
func (r *gluedProcess) next(buffer *[]byte) bool {
	if len(*buffer) < gpsize {
		return false
	}
	data := (*buffer)[:gpsize]
	*buffer = (*buffer)[gpsize:]
	r.pid = int32(binary.LittleEndian.Uint32(data[0:4]))
	r.ppid = int32(binary.LittleEndian.Uint32(data[4:8]))
	r.tim = int64(binary.LittleEndian.Uint64(data[8:16]))
	return true
}
func (r *gluedProcess) find() *process.Process {
	proc, _ := process.NewProcess(r.pid)
	match := gluedProcess{}
	match.fill(r.pid)
	if match.ppid <= 1 {
		match.ppid = r.ppid
	}
	if match.tim != r.tim || match.ppid != r.ppid {
		return nil
	}
	return proc
}
func (r *gluedProcess) fill(pid int32) (err error) {
	r.pid = pid
	proc, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	r.tim, _ = proc.CreateTime()
	r.ppid, _ = proc.Ppid()
	return
}

func procTrackerPath() string {
	return filepath.Join(config.Home(), "proc.tracker")
}

var glueMu sync.Mutex
var glueViaEnv = false

func init() {
	pid := int32(os.Getpid())
	proc, err := process.NewProcess(pid)
	if err == nil {
		env, err := proc.Environ()
		if err == nil && len(env) > 0 {
			glueViaEnv = true
		}
	}
}

func killOrhpansViaTracker() {
	pid := int32(os.Getpid())
	glueMu.Lock()
	defer glueMu.Unlock()

	// Load the list of tracked processes
	var list []gluedProcess
	data, err := os.ReadFile(procTrackerPath())
	if err == nil {
		for {
			var r gluedProcess
			if !r.next(&data) {
				break
			}
			list = append(list, r)
		}
	}

	// For each entry not belonging to the current process, kill it
	group := ProcessTree{}
	for _, r := range list {
		if r.ppid != pid {
			proc := r.find()
			if proc != nil {
				group.AddProcess(proc)
			}
		}
	}
	group.Kill()

	// Save the new list.
	list = lo.Filter(list, func(r gluedProcess, _ int) bool {
		return r.ppid == pid
	})
	if len(list) == 0 {
		os.Remove(procTrackerPath())
	} else {
		data = data[:0]
		for _, r := range list {
			data = append(data, r.toSlice()...)
		}
		os.WriteFile(procTrackerPath(), data, 0644)
	}
}
func killOrphansViaEnv() {
	group := ProcessTree{}
	for _, p := range lo.Must(process.Processes()) {
		if env, err := p.Environ(); err == nil {
			for _, e := range env {
				if strings.HasPrefix(e, "PM3G=") {
					group.AddProcess(p)
				}
			}
		}
	}
	group.Kill()
}
func KillOrphans() {
	if glueViaEnv {
		killOrphansViaEnv()
	} else {
		killOrhpansViaTracker()
	}
}

func glueChild(pid int) (err error) {
	if glueViaEnv {
		return nil
	}
	glueMu.Lock()
	defer glueMu.Unlock()

	proc := gluedProcess{}
	err = proc.fill(int32(pid))
	if err != nil {
		return err
	}

	file, err := os.OpenFile(procTrackerPath(), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	file.Write(proc.toSlice())
	return nil
}

type GluedCommand struct {
	*exec.Cmd
}

func (cmd GluedCommand) Start() (err error) {
	if glueViaEnv {
		cmd.Env = append(cmd.Env, "PM3G=1")
	}
	err = cmd.Cmd.Start()
	if cmd.Process != nil {
		glueChild(cmd.Process.Pid)
	}
	return
}
func (cmd GluedCommand) Run() (err error) {
	err = cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	return
}
