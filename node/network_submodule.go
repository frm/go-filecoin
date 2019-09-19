package node

import (
	"github.com/filecoin-project/go-filecoin/net"
	"github.com/libp2p/go-libp2p-core/routing"
)

// NetworkSubmodule enhances the `Node` with networking capabilities.
type NetworkSubmodule struct {
	Bootstrapper *net.Bootstrapper

	// PeerTracker maintains a list of peers good for fetching.
	PeerTracker *net.PeerTracker

	// Router is a router from IPFS
	Router routing.Routing
}
