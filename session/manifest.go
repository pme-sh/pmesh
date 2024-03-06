package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"get.pme.sh/pmesh/hosts"
	"get.pme.sh/pmesh/lyml"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/service"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xlog"

	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
)

type Server struct {
	vhttp.VirtualHostOptions `yaml:",inline"`
	Router                   vhttp.HandleMux `yaml:"router,omitempty"`
}

func (sv *Server) CreateVirtualHost() *vhttp.VirtualHost {
	vh := vhttp.NewVirtualHost(sv.VirtualHostOptions)
	vh.Mux.Routes = slices.Clone(sv.Router.Routes)
	return vh
}

type IPInfoOptions struct {
	Disable    bool     `yaml:"disable,omitempty"`
	MaxmindKey string   `yaml:"maxmind,omitempty"`
	Mark       []string `yaml:"mark,omitempty"`
}

func (i IPInfoOptions) CreateProvider() (info netx.IPInfoProvider) {
	if i.Disable {
		return netx.NullIPInfoProvider
	}

	info = netx.IP2ASNProvider
	if i.MaxmindKey != "" {
		info = netx.CombinedProvider{
			OrgPrimary: netx.NewMaxmindProvider(i.MaxmindKey),
			GeoPrimary: info,
		}
	}
	info = netx.CombinedProvider{
		OrgPrimary: netx.CloudflareProvider,
		GeoPrimary: info,
	}

	if len(i.Mark) > 0 {
		info = netx.NewMarkerProvider(info, i.Mark)
	}
	return
}

type JetStreamManifest struct {
	Stream    jetstream.StreamConfig              `yaml:"stream"`
	Consumers map[string]jetstream.ConsumerConfig `yaml:"consumers"`
}

func (j *JetStreamManifest) Init(ctx context.Context, name string, js jetstream.JetStream) error {
	if j.Stream.Name == "" {
		j.Stream.Name = name
	}
	s, err := js.CreateOrUpdateStream(ctx, j.Stream)
	if err != nil {
		return err
	}
	xlog.InfoC(ctx).Str("stream", j.Stream.Name).Msg("Stream created")
	for name, c := range j.Consumers {
		if c.Durable == "" {
			c.Durable = name
		}
		_, err := s.CreateOrUpdateConsumer(ctx, c)
		if err != nil {
			return err
		}
		xlog.InfoC(ctx).Str("stream", j.Stream.Name).Str("name", c.Durable).Msg("Consumer created")
	}
	return nil
}

type JetManifest struct {
	Streams map[string]JetStreamManifest           `yaml:"streams,omitempty"`
	KV      map[string]jetstream.KeyValueConfig    `yaml:"kv,omitempty"`
	Obj     map[string]jetstream.ObjectStoreConfig `yaml:"obj,omitempty"`
}

func (j *JetManifest) Init(ctx context.Context, js jetstream.JetStream) error {
	for name, s := range j.Streams {
		if err := s.Init(ctx, name, js); err != nil {
			return err
		}
	}
	for name, kv := range j.KV {
		if kv.Bucket == "" {
			kv.Bucket = name
		}
		_, err := js.CreateOrUpdateKeyValue(ctx, kv)
		if err != nil {
			return err
		}
		xlog.InfoC(ctx).Str("bucket", kv.Bucket).Msg("KV store created")
	}
	for name, obj := range j.Obj {
		if obj.Bucket == "" {
			obj.Bucket = name
		}
		_, err := js.CreateOrUpdateObjectStore(ctx, obj)
		if err != nil {
			return err
		}
		xlog.InfoC(ctx).Str("bucket", obj.Bucket).Msg("Object store created")
	}
	return nil
}

type HostsLine struct {
	Hostname string
	IP       string
}

func (h *HostsLine) UnmarshalYAML(node *yaml.Node) error {
	// Either map[string]ip or string [implicit localhost]
	if node.Kind == yaml.MappingNode && len(node.Content) == 2 {
		if e := node.Content[0].Decode(&h.Hostname); e != nil {
			return e
		}
		return node.Content[1].Decode(&h.IP)
	}
	h.IP = "127.0.0.1"
	return node.Decode(&h.Hostname)
}

type Manifest struct {
	Root         string                                   `yaml:"root,omitempty"`          // Root directory
	ServiceRoot  string                                   `yaml:"service_root,omitempty"`  // Service root directory
	Services     util.OrderedMap[string, service.Service] `yaml:"services,omitempty"`      // Services
	Server       map[string]*Server                       `yaml:"server,omitempty"`        // Virtual hosts
	IPInfo       IPInfoOptions                            `yaml:"ipinfo,omitempty"`        // IP information provider
	Env          map[string]string                        `yaml:"env,omitempty"`           // Environment variables
	Runners      map[string]*Runner                       `yaml:"runners,omitempty"`       // Runners
	Jet          JetManifest                              `yaml:"jet,omitempty"`           // JetStream configuration
	Hosts        []HostsLine                              `yaml:"hosts,omitempty"`         // Hostname to IP mapping
	CustomErrors string                                   `yaml:"custom_errors,omitempty"` // Path to custom error pages
}

func LoadManifest(manifestPath string) (*Manifest, error) {
	// Read the manifest
	var manifest Manifest
	if err := lyml.Load(manifestPath, &manifest); err != nil {
		return nil, err
	}

	// Prepare it
	if manifest.Root == "" {
		manifest.Root = filepath.Dir(manifestPath)
	} else if !filepath.IsAbs(manifest.Root) {
		manifest.Root = filepath.Join(filepath.Dir(manifestPath), manifest.Root)
	} else {
		manifest.Root = filepath.Clean(manifest.Root)
	}
	if manifest.ServiceRoot == "" {
		manifest.ServiceRoot = manifest.Root
	} else if !filepath.IsAbs(manifest.ServiceRoot) {
		manifest.ServiceRoot = filepath.Join(filepath.Dir(manifestPath), manifest.ServiceRoot)
	} else {
		manifest.ServiceRoot = filepath.Clean(manifest.ServiceRoot)
	}

	// Set hosts
	mapping := hosts.Mapping{}
	for _, h := range manifest.Hosts {
		mapping[h.Hostname] = h.IP
	}
	if err := hosts.Insert(mapping); err != nil {
		xlog.Err(err).Msg("Failed to update hosts file")
	}

	// Set env
	for k, v := range manifest.Env {
		os.Setenv(k, v)
	}
	os.Setenv("PM3_ROOT", manifest.Root)

	for _, tup := range manifest.Services {
		name, s := tup.A, tup.B
		err := s.Prepare(service.Options{
			Name:        name,
			ServiceRoot: manifest.ServiceRoot,
			Logger:      xlog.NewDomain(name),
		})
		if err != nil {
			return nil, err
		}
	}
	for name, sv := range manifest.Server {
		for _, str := range strings.Split(name, ",") {
			name = strings.TrimSpace(str)
			if name == "" {
				continue
			}
			sv.Hostnames = append(sv.Hostnames, name)
		}
	}
	return &manifest, nil
}
