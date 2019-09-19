package node

import (
	"github.com/filecoin-project/go-filecoin/message"
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
	// Review: I guess this is either Node or a subsystem for managing protocols
	VersionTable version.ProtocolVersionTable

	// Review: candidate to be moved back to Node
	PorcelainAPI *porcelain.API
	// Repo is the repo this node was created with
	// it contains all persistent artifacts of the filecoin node
	// Review: candidate to be moved back to Node
	Repo repo.Repo

	// Review: I need to better understand this guy..
	// Incoming messages for block mining.
	Inbox *message.Inbox
	// Messages sent and not yet mined.
	Outbox *message.Outbox

	// Review: is this its own submodule?
	Wallet *wallet.Wallet

	// Review: is this only used for storage deals or retrieval too?
	//         if its only storage -> move to StorageSubmodule
	//         if its both, move to SectorBuilderSubmodule?
	// Exchange is the interface for fetching data from other nodes.
	Exchange exchange.Interface

	// Review: where do we place this guy?
	// Blockservice is a higher level interface for fetching data
	blockservice bserv.BlockService

	// Review: where do we place this guy?
	// Blockstore is the un-networked blocks interface
	Blockstore bstore.Blockstore

	// Review: should we move this to the chain submodule?
	// TODO: cache for chain and data shared, wrapper for blockstore
	// CborStore is a temporary interface for interacting with IPLD objects.
	cborStore *hamt.CborIpldStore
}
