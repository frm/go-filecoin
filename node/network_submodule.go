package node

import "github.com/filecoin-project/go-filecoin/net"

// NetworkSubmodule enhances the `Node` with networking capabilities.
type NetworkSubmodule struct {
	Bootstrapper *net.Bootstrapper
	// PeerTracker maintains a list of peers good for fetching.
	PeerTracker *net.PeerTracker
}
