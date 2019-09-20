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
	// TODO: move to node
	VersionTable version.ProtocolVersionTable

	// TODO: move to node
	PorcelainAPI *porcelain.API

	// Repo is the repo this node was created with
	// it contains all persistent artifacts of the filecoin node
	//
	// TODO: move to store, although each submodule should only see a view of it
	Repo repo.Repo

	// Incoming messages for block mining.
	//
	// TODO: have this two form the `MessagingSubmodule` (issue: ???)
	Inbox *message.Inbox
	// Messages sent and not yet mined.
	Outbox *message.Outbox

	// TODO: Move too its own submodule
	Wallet *wallet.Wallet

	// Exchange is the interface for fetching data from other nodes.
	//
	// TODO: move to a `StorageNetworkingSubmodule`
	Exchange exchange.Interface

	// Blockservice is a higher level interface for fetching data
	//
	// Note: at present `BlockService` is shared by chain/graphsync and piece/bitswap data
	// TODO: split chain data from piece data (issue: ???)
	blockservice bserv.BlockService

	// Blockstore is the un-networked blocks interface
	//
	// Note: at present `Blockstore` is shared by chain/graphsync and piece/bitswap data
	// TODO: split chain data from piece data (issue: ???)
	Blockstore bstore.Blockstore

	// CborStore is a temporary interface for interacting with IPLD objects.
	//
	// Note: Used for chain state and shared with piece data exchange for deals at the moment.
	// TODO: split chain data from piece data (issue: ???)
	cborStore *hamt.CborIpldStore
}
