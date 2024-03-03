package vhttp

import (
	"encoding/json"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type patternMatcher interface {
	Match(host, path string) bool
}

// ~foo
type patternRegex struct {
	rule *regexp.Regexp
}

func (p patternRegex) Match(host, path string) bool {
	return p.rule.MatchString(host + path)
}

// foo/
// foo+
type patternPrefix struct {
	prefix string
}

func (p patternPrefix) Match(host, path string) bool {
	patternLen, hostLen := len(p.prefix), len(host)
	if patternLen <= hostLen {
		return host[:patternLen] == p.prefix
	}
	if host != p.prefix[:hostLen] {
		return false
	}
	pfx := p.prefix[hostLen:]
	return strings.HasPrefix(path, pfx)
}

// +foo
type patternSuffix struct {
	suffix string
}

func (p patternSuffix) Match(host, path string) bool {
	patternLen, pathLen := len(p.suffix), len(path)
	if patternLen <= pathLen {
		return path[pathLen-patternLen:] == p.suffix
	}
	if path != p.suffix[pathLen:] {
		return false
	}
	sfx := p.suffix[:pathLen]
	return strings.HasSuffix(host, sfx)
}

// foo
type patternExact struct {
	exact string
}

func (p patternExact) Match(host, path string) bool {
	patternLen, hostLen, pathLen := len(p.exact), len(host), len(path)
	if patternLen != (hostLen + pathLen) {
		return false
	}
	return host == p.exact[:hostLen] && path == p.exact[hostLen:]
}

// foo, bar
type patternOr struct {
	set []patternMatcher
}

func (p patternOr) Match(host, path string) bool {
	for _, m := range p.set {
		if m.Match(host, path) {
			return true
		}
	}
	return false
}

type Pattern struct {
	patternMatcher
	str string
}

func NewPattern(v string) (p Pattern, e error) {
	e = p.UnmarshalText([]byte(v))
	return
}
func NewExactPattern(v string) (p Pattern) {
	p.patternMatcher = patternExact{v}
	p.str = v
	return
}

func newPatternSet(v []string) (p []patternMatcher, e error) {
	p = make([]patternMatcher, 0, len(v))
	for _, s := range v {
		ptrn, e := NewPattern(s)
		if e != nil {
			return nil, e
		}
		if ptrn.patternMatcher == nil {
			continue
		}
		p = append(p, ptrn.patternMatcher)
	}
	return
}
func (p Pattern) String() string {
	return p.str
}
func (p Pattern) IsWildcard() bool {
	return p.patternMatcher == nil
}
func (p Pattern) MarshalText() ([]byte, error) {
	return []byte(p.str), nil
}
func (p *Pattern) UnmarshalText(data []byte) (e error) {
	prevSpace := false
	p.str = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0:
			if prevSpace {
				return -1
			}
			prevSpace = true
			return ' '
		default:
			prevSpace = false
			return r
		}
	}, strings.TrimSpace(string(data)))

	switch {
	case p.str == "_" || p.str == "":
		p.patternMatcher = nil
	case strings.Contains(p.str, ", "):
		var pattern patternOr
		pattern.set, e = newPatternSet(strings.Split(p.str, ", "))
		p.patternMatcher = pattern
	case strings.HasPrefix(p.str, "~"):
		var pattern patternRegex
		pattern.rule, e = regexp.Compile(p.str[1:])
		p.patternMatcher = pattern
	case strings.HasPrefix(p.str, "+"):
		p.patternMatcher = patternSuffix{p.str[1:]}
	case strings.HasSuffix(p.str, "+"):
		p.patternMatcher = patternPrefix{p.str[:len(p.str)-1]}
	case strings.HasSuffix(p.str, "/"):
		p.patternMatcher = patternPrefix{p.str}
	default:
		p.patternMatcher = patternExact{p.str}
	}
	return
}
func (p Pattern) MarshalYAML() (any, error) {
	return p.str, nil
}
func (p Pattern) MarshalJSON() ([]byte, error) {
	return p.MarshalText()
}
func (p *Pattern) UnmarshalYAML(node *yaml.Node) (err error) {
	if node.Kind == yaml.SequenceNode {
		var rest []string
		if err = node.Decode(&rest); err == nil {
			var pattern patternOr
			pattern.set, err = newPatternSet(rest)
			p.patternMatcher = pattern
			p.str = strings.Join(rest, " ")
		}
		return
	} else {
		var res string
		if err = node.Decode(&res); err != nil {
			return
		}
		return p.UnmarshalText([]byte(res))
	}
}
func (p *Pattern) UnmarshalJSON(data []byte) (err error) {
	if len(data) > 0 && data[0] == '[' {
		var rest []string
		if err = json.Unmarshal(data, &rest); err == nil {
			var pattern patternOr
			pattern.set, err = newPatternSet(rest)
			p.patternMatcher = pattern
			p.str = strings.Join(rest, ", ")
		}
		return
	} else {
		var res string
		if err = json.Unmarshal(data, &res); err != nil {
			return
		}
		return p.UnmarshalText([]byte(res))
	}
}
func (p *Pattern) Match(host, path string) bool {
	return p.patternMatcher == nil || p.patternMatcher.Match(host, path)
}
