package hosts

import (
	"fmt"
	"slices"
	"strings"
)

type Hostname = string
type Address = string
type EntryLine struct {
	OriginalLine string
	Address      Address
	Hostnames    []Hostname
	Comment      string
}

func (h EntryLine) HasHost(host Hostname) bool {
	for _, h := range h.Hostnames {
		if h == host {
			return true
		}
	}
	return false
}
func (h EntryLine) String() string {
	if h.OriginalLine != "" {
		return h.OriginalLine
	}
	if h.Comment == "" {
		if len(h.Hostnames) == 0 {
			return h.Address
		}
		return fmt.Sprintf("%s %s", h.Address, strings.Join(h.Hostnames, " "))
	} else {
		return fmt.Sprintf("%s %s # %s", h.Address, strings.Join(h.Hostnames, " "), h.Comment)
	}
}
func (h EntryLine) MarhalText() ([]byte, error) {
	return []byte(h.String()), nil
}
func (h *EntryLine) UnmarshalText(data []byte) error {
	h.OriginalLine = string(data)
	host, comment, found := strings.Cut(string(data), "#")
	if !found {
		host = string(data)
	} else {
		h.Comment = strings.TrimSpace(comment)
	}
	adr, hostnames, _ := strings.Cut(host, " ")
	h.Address = strings.TrimSpace(adr)
	h.Hostnames = append(h.Hostnames, strings.Fields(hostnames)...)
	return nil
}
func (h EntryLine) Equals(other EntryLine) bool {
	if h.OriginalLine != other.OriginalLine {
		return false
	} else if h.OriginalLine == "" {
		return h.Address == other.Address &&
			h.Comment == other.Comment &&
			slices.Equal(h.Hostnames, other.Hostnames)
	}
	return true
}

type Config []EntryLine
type Mapping map[Hostname]Address

func (h Config) resolveLine(host string) int {
	for i, entry := range h {
		if entry.HasHost(host) {
			return i
		}
	}
	return -1
}
func (h Config) Resolve(host Hostname) (adr Address, ok bool) {
	line := h.resolveLine(host)
	if line != -1 {
		return h[line].Address, true
	}
	return
}
func (h *Config) Insert(data Mapping) (changed bool) {
	// Check if this operation is redundant
	for host, adr := range data {
		if ip, _ := h.Resolve(host); ip != adr {
			changed = true
			break
		}
	}
	if !changed {
		return
	}

	// We only edit the lines with #pmesh comments
	// first create a complete mapping.
	//
	mapping := make(Mapping, len(*h))
	result := make([]EntryLine, 0, len(*h))
	for _, entry := range *h {
		if entry.Comment != "" && strings.HasPrefix(entry.Comment, "pmesh") {
			for _, host := range entry.Hostnames {
				mapping[host] = entry.Address
			}
		} else {
			result = append(result, entry)
		}
	}

	// Add the new entries
	for host, adr := range data {
		adr = strings.TrimSpace(adr)
		host = strings.TrimSpace(host)
		if host != "" {
			mapping[host] = adr
		}
	}

	// Create the new entries
	for host, adr := range mapping {
		result = append(result, EntryLine{"", adr, []Hostname{host}, "pmesh"})
	}

	// Replace the hosts file
	*h = result
	return
}
func (h Config) String() string {
	var b strings.Builder
	for _, entry := range h {
		b.WriteString(entry.String())
		b.WriteRune('\n')
	}
	return b.String()
}
func (h Config) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}
func (h *Config) UnmarshalText(data []byte) error {
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var entry EntryLine
		if err := entry.UnmarshalText([]byte(line)); err != nil {
			return err
		}
		*h = append(*h, entry)
	}
	return nil
}
func (h *Config) Equals(other Config) bool {
	if len(*h) != len(other) {
		return false
	}
	for i, entry := range *h {
		if !entry.Equals(other[i]) {
			return false
		}
	}
	return true
}
