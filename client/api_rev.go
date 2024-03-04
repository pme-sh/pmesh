package client

import (
	"get.pme.sh/pmesh/session"
)

func (c Client) GetRepoInfo() (info session.RepoInfo, err error) {
	err = c.Call("/repo", nil, &info)
	return
}
func (c Client) UpdateRepo(p session.UpdateParams) (res session.PullResult, err error) {
	err = c.Call("/repo/update", p, &res)
	return
}
