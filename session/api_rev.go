package session

import (
	"context"
	"net/http"
	"sync"
	"time"

	"get.pme.sh/pmesh/revision"
)

type RepoInfo struct {
	Sys    string             `json:"sys"`    // Repository system
	Ref    revision.Reference `json:"ref"`    // Repository reference
	Remote revision.Reference `json:"remote"` // Remote repository reference
	URL    string             `json:"url"`    // Remote repository URL
}
type PullResult struct {
	From    revision.Reference `json:"from"`    // From reference
	To      revision.Reference `json:"to"`      // To reference
	Changed bool               `json:"changed"` // True if the repository was changed
}
type UpdateParams struct {
	Invalidate bool `json:"invalidate"` // True if the session should be invalidated
}

var repoLock sync.Mutex
var lastFetch time.Time

func getRepoState(session *Session, ctx context.Context, fetch bool) (res revision.Repo, err error) {
	repoLock.Lock()
	defer repoLock.Unlock()

	res, err = revision.Open(session.Manifest().Root)
	if err != nil {
		return
	}
	if fetch {
		if time.Since(lastFetch) < 5*time.Second {
			return
		}
		lastFetch = time.Now()
		res.Fetch(ctx)
	}
	return
}

func init() {
	Match("/repo", func(session *Session, r *http.Request, p struct{}) (info RepoInfo, err error) {
		repo, err := getRepoState(session, r.Context(), false)
		if err != nil {
			return
		}
		info.Sys = repo.System()
		info.Ref, _ = repo.Head()
		info.Remote, _ = repo.RemoteHead()
		info.URL, _ = repo.RemoteURL()
		return
	})
	Match("/repo/update", func(session *Session, r *http.Request, p UpdateParams) (res PullResult, err error) {
		repo, err := getRepoState(session, r.Context(), true)
		if err != nil {
			return
		}

		head, err := repo.Head()
		if err != nil {
			return
		}
		res.From = head

		remote, _ := repo.RemoteHead()
		res.To = remote
		if remote.Hash == "" {
			res.To = head
			return
		}
		if res.From.Hash == res.To.Hash {
			return
		}
		err = repo.Update()
		if err == nil {
			go session.Reload(p.Invalidate)
		}
		return
	})
}
