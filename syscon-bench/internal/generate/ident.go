package generate

import "fmt"

// PoolSize is the number of instance identities. Workers are not instances.
const PoolSize = 1000

type identity struct {
	instanceID string
	host       string
	ip         string
}

var identities = func() []identity {
	out := make([]identity, PoolSize)
	for i := 0; i < PoolSize; i++ {
		out[i] = identityAt(i)
	}
	return out
}()

func identityAt(i int) identity {
	n := fmt.Sprintf("%04d", i)
	return identity{
		instanceID: "instance-" + n,
		host:       "host-" + n,
		ip:         fmt.Sprintf("10.%d.%d.10", i/256, i%256),
	}
}
