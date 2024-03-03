package config

import (
	"fmt"

	"github.com/samber/lo"
)

type Role int

const (
	RoleNotSet Role = iota
	RoleSeed
	RoleNode
	RoleReplica
	RoleClient
)

var roleStr = map[string]Role{
	"":        RoleNotSet,
	"seed":    RoleSeed,
	"node":    RoleNode,
	"replica": RoleReplica,
	"client":  RoleClient,
}
var roleInt = lo.Invert(roleStr)

func (k Role) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}
func (k *Role) UnmarshalText(text []byte) error {
	v, ok := roleStr[string(text)]
	if !ok {
		return fmt.Errorf("unknown role: %q", text)
	}
	*k = v
	return nil
}
func (k Role) String() string {
	return roleInt[k]
}
