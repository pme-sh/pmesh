package vhttp

import (
	"path"
	"strings"
)

func CleanPath(s string) string {
	s = path.Clean(s)
	if s == "." {
		return "/"
	} else if !strings.HasPrefix(s, "/") {
		return "/" + s
	}
	return s
}
func NormalPath(s string) string {
	// Ensure there's a leading slash
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}

	// Remove trailing slash
	s = strings.TrimSuffix(s, "/")

	// Remove double slashes
	if strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return s
}
func RelativePath(root, rel string) string {
	return CleanPath(path.Join(root, rel))
}
