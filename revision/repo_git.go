package revision

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

type GitRepo struct {
	*git.Repository
}

func (r GitRepo) getRemote() (*git.Remote, error) {
	remotes, e := r.Repository.Remotes()
	if e != nil {
		return nil, e
	}
	if len(remotes) == 0 {
		return nil, os.ErrNotExist
	}
	return remotes[0], nil
}
func (r GitRepo) convertReference(ref *plumbing.Reference) Reference {
	res := Reference{
		Branch: ref.Name().Short(),
		Hash:   ref.Hash().String(),
	}
	if commit, err := r.CommitObject(ref.Hash()); err == nil {
		res.Author = commit.Author.String()
		res.Message = commit.Message
	}
	return res
}

func (GitRepo) System() string { return GitSystem{}.Name() }
func (r GitRepo) Head() (Reference, error) {
	ref, e := r.Repository.Head()
	if e != nil {
		return Reference{}, e
	}
	return r.convertReference(ref), nil
}
func (r GitRepo) RemoteURL() (string, error) {
	remote, e := r.getRemote()
	if e != nil {
		return "", e
	}
	return remote.Config().URLs[0], nil
}
func (r GitRepo) RemoteHead() (Reference, error) {
	ref, e := r.Repository.Head()
	if e != nil {
		return Reference{}, e
	}
	if ref.Name().IsRemote() {
		return r.convertReference(ref), nil
	}
	if ref.Name().IsBranch() {
		remote, e := r.getRemote()
		if e != nil {
			return Reference{}, e
		}
		remoteRef, e := r.Repository.Reference(plumbing.NewRemoteReferenceName(remote.Config().Name, ref.Name().Short()), true)
		if e != nil {
			return Reference{}, e
		}
		return r.convertReference(remoteRef), nil
	}
	return Reference{}, errors.New("no remote branch")
}
func (r GitRepo) Fetch(ctx context.Context) (err error) {
	remote, e := r.getRemote()
	if e != nil {
		return e
	}

	err = r.FetchContext(ctx, &git.FetchOptions{RemoteName: remote.Config().Name})
	if err == git.NoErrAlreadyUpToDate {
		err = nil
	} else if err != nil {
		var w *git.Worktree
		w, err = r.Worktree()
		if err != nil {
			return err
		}

		cmd := exec.CommandContext(ctx, "git", "fetch")
		cmd.Dir = w.Filesystem.Root()
		err = cmd.Run()
	}
	return
}
func (r GitRepo) Update() error {
	remote, err := r.RemoteHead()
	if err != nil {
		return err
	}
	local, err := r.Head()
	if err != nil {
		return err
	}
	if remote.Hash == local.Hash {
		return nil
	}
	return exec.Command("git", "reset", "--hard", remote.Hash).Run()

	// NOTE: go-git does not implement this properly and removes untracked files
	//
	//w, e := r.Worktree()
	//if e != nil {
	//	return e
	//}
	//return w.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: plumbing.NewHash(remote.Hash)})
}

type GitSystem struct{}

func (s GitSystem) Name() string {
	return "git"
}
func (s GitSystem) Open(path string) (Repo, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return nil, os.ErrNotExist
	}
	r, e := git.PlainOpen(path)
	if e != nil {
		return nil, e
	}
	return GitRepo{r}, nil
}

func init() {
	Systems["git"] = GitSystem{}
}
