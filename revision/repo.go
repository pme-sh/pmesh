package revision

import (
	"context"
	"errors"
	"path/filepath"
)

type Reference struct {
	Branch  string `json:"branch"`
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Message string `json:"message"`
}

type Repo interface {
	System() string
	Head() (Reference, error)
	RemoteHead() (Reference, error)
	RemoteURL() (string, error)
	Fetch(ctx context.Context) error
	Update() error
}

type System interface {
	Name() string
	Open(path string) (Repo, error)
}

var Systems = map[string]System{}

func Open(path string) (r Repo, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	for {
		for _, s := range Systems {
			r, e := s.Open(path)
			if e == nil {
				return r, nil
			}
		}

		prev := path
		path = filepath.Dir(path)
		if path == "/" || path == prev {
			break
		}
	}
	return nil, errors.New("no repository found")
}
