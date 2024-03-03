package hosts

import (
	"strings"
)

// google.com, www.google.com -> www., true
// google.com, google.com -> "", true
// google.com, yahoo.com -> "", false
func Match(root, needle string) (sub string, ok bool) {
	at := len(needle) - len(root)
	if at >= 0 && needle[at:] == root {
		if at == 0 {
			return "", true
		}
		if needle[at-1] == '.' {
			return needle[:at], true
		}
	}
	return
}

// "www.google.com" ""
// "google.com" "www."
// "com" "www.google."
func Range(v string, f func(root, sub string) bool) bool {
	offset := 0
	sub := ""
	for {
		key := v[offset:]
		if !f(key, sub) {
			return false
		}

		next := strings.IndexByte(key, '.') + 1
		if next == 0 {
			break
		}
		sub = v[:offset+next]
		offset += next
	}
	return true
}
func MatchMap[V any](m map[string]V, needle string) (root, sub string, val V) {
	if m == nil {
		return
	}
	Range(needle, func(k, s string) bool {
		var ok bool
		if val, ok = m[k]; ok {
			root, sub = k, s
			return false
		}
		return true
	})
	return
}
func TestMap[V any](m map[string]V, needle string) (ok bool) {
	root, _, _ := MatchMap(m, needle)
	return root != ""
}

var localhosts = map[string]bool{
	"localhost": true,
	"[::1]":     true,
	"127.0.0.1": true,
}

func IsLocal(host string) bool {
	return strings.HasSuffix(host, ".local") ||
		localhosts[host]
}

func IsPrivate(host string) bool {
	return strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local") ||
		localhosts[host]
}
