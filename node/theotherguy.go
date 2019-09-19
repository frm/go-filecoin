package node

import (
	"github.com/filecoin-project/go-filecoin/message"
	"github.com/filecoin-project/go-filecoin/net"
	"github.com/filecoin-project/go-filecoin/porcelain"
	"github.com/filecoin-project/go-filecoin/repo"
	"github.com/filecoin-project/go-filecoin/version"
	"github.com/filecoin-project/go-filecoin/wallet"
	bserv "github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-hamt-ipld"
	bstore "github.com/ipfs/go-ipfs-blockstore"
	exchange "github.com/ipfs/go-ipfs-exchange-interface"
)

// ToSplitOrNotToSplitNode is part of an ongoing refactor to cleanup `node.Node`.
//
// This structure will not stay, everything in here will either:
// - be moved back to node,
// - be moved into an existing submodule
// - form a new submodule
//
// TODO: clean this up to complete the refactor https://github.com/filecoin-project/go-filecoin/issues/3140
type ToSplitOrNotToSplitNode struct {
	VersionTable version.ProtocolVersionTable

	PorcelainAPI *porcelain.API
	// Repo is the repo this node was created with
	// it contains all persistent artifacts of the filecoin node
	Repo repo.Repo

	// Review: is this message queue only used for block mining?
	// Incoming messages for block mining.
	Inbox *message.Inbox
	// Messages sent and not yet mined.
	Outbox *message.Outbox

	Wallet *wallet.Wallet

	// TODO: network networking
	Bootstrapper *net.Bootstrapper
	// PeerTracker maintains a list of peers good for fetching.
	PeerTracker *net.PeerTracker

	// TODO: this is more on the chainsync networking
	// Fetcher is the interface for fetching data from nodes.
	Fetcher net.Fetcher

	// TODO: this is more on the storage dealing networking side
	// Exchange is the interface for fetching data from other nodes.
	Exchange exchange.Interface
	// Blockservice is a higher level interface for fetching data
	blockservice bserv.BlockService

	// Review: check what this guy is about exactly
	// Blockstore is the un-networked blocks interface
	Blockstore bstore.Blockstore

	// Review: should we move this to the chain submodule?
	// TODO: cache for chain and data shared, wrapper for blockstore
	// CborStore is a temporary interface for interacting with IPLD objects.
	cborStore *hamt.CborIpldStore
}
