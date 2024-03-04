package vhttp

import (
	"path"
	"strings"
)

func CleanPath(s string) string {
	hasTrailSlash := strings.HasSuffix(s, "/")
	s = path.Clean(s)
	if s == "." {
		return "/"
	}

	// Ensure there's a leading slash
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}

	// Add trailing slash back
	if hasTrailSlash && !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}
func NormalPath(s string) string {
	// Ensure there's a leading slash
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}

	// Remove double slashes
	if strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return s
}
func RelativePath(root, rel string) string {
	return CleanPath(path.Join(root, rel))
}
