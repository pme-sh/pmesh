package vhttp

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pme-sh/pmesh/variant"

	"gopkg.in/yaml.v3"
)

type RewriteHandler struct {
	Match   *regexp.Regexp
	Replace string
}

func (rh RewriteHandler) String() string {
	return fmt.Sprintf("Rewrite %s %s", rh.Match.String(), rh.Replace)
}
func (rh *RewriteHandler) UnmarshalText(text []byte) error {
	return rh.UnmarshalInline(string(text))
}
func (rh *RewriteHandler) UnmarshalInline(text string) error {
	if !strings.HasPrefix(text, "rewrite ") {
		return variant.RejectMatch(rh)
	}
	var match string
	_, err := fmt.Sscanf(text, "rewrite %s %s", &match, &rh.Replace)
	if err != nil {
		return fmt.Errorf("invalid rewrite format: %q", text)
	}
	rh.Match, err = regexp.Compile(match)
	if err != nil {
		return fmt.Errorf("invalid rewrite pattern: %s", err)
	}
	return nil
}
func (rh *RewriteHandler) UnmarshalYAML(node *yaml.Node) (err error) {
	if node.Kind != yaml.ScalarNode {
		var tmp struct {
			Match   regexp.Regexp `yaml:"match"`
			Replace string        `yaml:"replace"`
		}
		if err = node.Decode(&tmp); err != nil {
			return
		}
		rh.Match = &tmp.Match
		rh.Replace = tmp.Replace
		return
	} else {
		var text string
		if err = node.Decode(&text); err != nil {
			return
		}
		return rh.UnmarshalInline(text)
	}
}

func (rh RewriteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	r.URL.Path = CleanPath(rh.Match.ReplaceAllString(r.URL.Path, rh.Replace))
	return Continue
}

func init() {
	Registry.Define("Rewrite", func() any { return &RewriteHandler{} })
}
