package xlog

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

// XLog uses the caller field in zerolog to record what we call "domains" which are lazily registrable pub/sub queue
// paths that uniquely identify what it is that the logger is currently logging about. E.g.
// pmesh
// myfrontend
// myfrontend.pid
const (
	DomainFieldName = "dom"
)

// Collector is an interface for collecting log messages in real-time.
type Collector interface {
	Write(p []byte, level Level, domain string)
}

var globalCollectors = []Collector{}
var globalCollectorsMutex = sync.RWMutex{}

func RegisterCollector(c Collector) {
	globalCollectorsMutex.Lock()
	defer globalCollectorsMutex.Unlock()
	globalCollectors = append(globalCollectors, c)
}
func RemoveCollector(c Collector) {
	globalCollectorsMutex.Lock()
	defer globalCollectorsMutex.Unlock()
	globalCollectors = lo.Without(globalCollectors, c)
}

// Domain represents a domain.
type Domain struct {
	name        string
	encodedName []byte // JSON escaped name
	logger      Logger
}

func (d *Domain) String() string { return d.name }

// Implement zerolog.Hook
func (d *Domain) Run(e *Event, level Level, msg string) {
	e.Timestamp()
	if e.Enabled() {
		e.RawJSON(DomainFieldName, d.encodedName)
	}
}

// Implement zerolog.MultilevelWriter
func (d *Domain) Write(p []byte) (n int, err error) {
	return d.WriteLevel(LevelInfo, p)
}
func (d *Domain) WriteLevel(l Level, p []byte) (n int, err error) {
	globalCollectorsMutex.RLock()
	defer globalCollectorsMutex.RUnlock()
	for _, c := range globalCollectors {
		c.Write(p, l, d.name)
	}
	return len(p), nil
}

type domainKey struct{}

// ContextDomain returns the domain from the context.
func ContextDomain(c context.Context) *Domain {
	if d, ok := c.Value(domainKey{}).(*Domain); ok {
		return d
	}
	return nil
}

// NewDomain creates a new domain and a logger.
func NewDomain(name string, w ...io.Writer) (l *Logger) {
	if len(w) == 0 {
		w = append(w, DefaultWriter{})
	}
	dom := &Domain{name: name}
	w = append(w, dom)
	dom.encodedName, _ = json.Marshal(name)
	dom.logger = zerolog.New(zerolog.MultiLevelWriter(w...)).Hook(dom)
	return &dom.logger
}
