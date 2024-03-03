package service

import (
	"sync"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/subnet"
)

var SubnetAllocator = sync.OnceValue(func() *subnet.Allocator {
	return subnet.NewAllocator(*config.ServiceSubnet, true)
})
