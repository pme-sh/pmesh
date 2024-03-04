package service

import (
	"sync"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/subnet"
)

var SubnetAllocator = sync.OnceValue(func() *subnet.Allocator {
	return subnet.NewAllocator(*config.ServiceSubnet, true)
})
