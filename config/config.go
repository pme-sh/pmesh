package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	atomicfile "github.com/natefinch/atomic"
)

type Config struct {
	Role       Role                `json:"role"`       // Role of this server
	Host       string              `json:"host"`       // Hostname of this server
	Cluster    string              `json:"cluster"`    // Cluster name
	Secret     string              `json:"secret"`     // Secret key used for all encryption
	Topology   map[string][]string `json:"topology"`   // Topology of the mesh [Hostname -> Cluster]
	Advertised string              `json:"advertised"` // Advertised hostname of this server
	PeerUD     map[string]any      `json:"peerud"`     // Arbitrary data to be sent to peers
	LocalUD    map[string]any      `json:"localud"`    // Arbitrary data used for parsing yaml
}

func (c *Config) SetDefaults() {
	if c.Topology == nil {
		c.Topology = map[string][]string{}
	}
	if c.PeerUD == nil {
		c.PeerUD = map[string]any{}
	}
	if c.LocalUD == nil {
		c.LocalUD = map[string]any{}
	}
	if c.Host == "" {
		c.Host, _ = os.Hostname()
		c.Host, _, _ = strings.Cut(c.Host, ".")
		c.Host = strings.ToLower(c.Host)
		c.Host = strings.TrimSuffix(c.Host, "s-macbook-pro")
		c.Host = strings.TrimSuffix(c.Host, "s-macbook")
		c.Host = strings.TrimPrefix(c.Host, "desktop-")
		c.Host = strings.TrimPrefix(c.Host, "laptop-")
	}
	if c.Secret == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic(err)
		}
		c.Secret = strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret))
	}
}

func configPath() string {
	return filepath.Join(Home(), "config.json")
}
func readConfig() (out Config, err error) {
	data, err := os.ReadFile(configPath())
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if len(data) != 0 {
		err = json.Unmarshal(data, &out)
	} else {
		err = nil
	}
	out.SetDefaults()
	return
}
func writeConfigLocked(in *Config) error {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(configPath(), bytes.NewReader(data))
}

var settings atomic.Pointer[Config]

func Update(update func(*Config) error) error {
	return WithLock(func() error {
		c, err := readConfig()
		c.SetDefaults()
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			if update != nil {
				if err = update(&c); err != nil {
					return err
				}
			}
			if err = writeConfigLocked(&c); err != nil {
				return err
			}
			settings.Store(&c)
		} else {
			if update != nil {
				if err = update(&c); err != nil {
					return err
				}
				if err = writeConfigLocked(&c); err != nil {
					return err
				}
			}
			settings.Store(&c)
		}
		return nil
	})
}

func Get() *Config {
	res := settings.Load()
	if res == nil {
		c, err := readConfig()
		if err != nil {
			panic(err)
		}
		c.SetDefaults()
		settings.Store(&c)
		return &c
	}
	return res
}
