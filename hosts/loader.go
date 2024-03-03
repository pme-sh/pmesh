package hosts

import (
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

var configPath string

func init() {
	configPath = "/etc/hosts"
	if runtime.GOOS == "windows" {
		configPath = os.ExpandEnv("${SystemRoot}\\System32\\drivers\\etc\\hosts")
	}
}
func openConfig(r bool) (f *os.File, e error) {
	if r {
		return os.OpenFile(configPath, os.O_RDONLY, 0644)
	} else {
		f, e = os.OpenFile(configPath, os.O_RDWR|os.O_CREATE, 0644|os.ModeExclusive)
		if e != nil {
			f, e = os.OpenFile(configPath, os.O_RDWR|os.O_CREATE, 0644)
		}
		return
	}
}

var systemConfig Config
var systemConfigUpdatedAt time.Time
var configLock sync.Mutex

func refreshSystemConfigLocked(file *os.File) (err error) {
	if systemConfig != nil && time.Since(systemConfigUpdatedAt) < time.Second {
		return nil
	}
	if file == nil {
		file, err = openConfig(true)
		if err != nil {
			return err
		}
		defer file.Close()
	}
	if stat, e := file.Stat(); e == nil && stat.ModTime().Before(systemConfigUpdatedAt) {
		return nil
	}

	data, err := io.ReadAll(file)
	if err != nil {
		if os.IsNotExist(err) {
			systemConfig = systemConfig[:0]
			systemConfigUpdatedAt = time.Now()
			return nil
		}
		return err
	}

	systemConfig = systemConfig[:0]
	err = systemConfig.UnmarshalText(data)
	systemConfigUpdatedAt = time.Now()
	return
}

func SystemConfig() (cfg Config, err error) {
	configLock.Lock()
	defer configLock.Unlock()
	err = refreshSystemConfigLocked(nil)
	return systemConfig, err
}

var ErrAbort = errors.New("abort") // used to abort an update
func UpdateSystemConfig(cb func(*Config) (err error)) error {
	configLock.Lock()
	defer configLock.Unlock()
	file, err := openConfig(false)
	if err != nil {
		return err
	}
	defer file.Close()

	err = refreshSystemConfigLocked(file)
	if err != nil {
		return err
	}

	backup := make(Config, len(systemConfig))
	copy(backup, systemConfig)

	err = cb(&systemConfig)
	if err == ErrAbort {
		systemConfig = backup
		systemConfigUpdatedAt = time.Now()
		err = nil
	} else if !systemConfig.Equals(backup) {
		if err == nil {
			file.Seek(0, 0)
			file.Truncate(0)

			var data []byte
			if data, err = systemConfig.MarshalText(); err == nil {
				_, err = file.Write(data)
			}
		}
		if err != nil {
			systemConfig = backup
		} else {
			systemConfigUpdatedAt = time.Now()
		}
	}
	return err
}
