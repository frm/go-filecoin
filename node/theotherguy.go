package node

import (
	"context"
	"sync"

	"github.com/filecoin-project/go-filecoin/chain"
	"github.com/filecoin-project/go-filecoin/consensus"
	"github.com/filecoin-project/go-filecoin/message"
	"github.com/filecoin-project/go-filecoin/mining"
	"github.com/filecoin-project/go-filecoin/net"
	"github.com/filecoin-project/go-filecoin/porcelain"
	"github.com/filecoin-project/go-filecoin/proofs/sectorbuilder"
	"github.com/filecoin-project/go-filecoin/protocol/block"
	"github.com/filecoin-project/go-filecoin/protocol/hello"
	"github.com/filecoin-project/go-filecoin/protocol/retrieval"
	"github.com/filecoin-project/go-filecoin/protocol/storage"
	"github.com/filecoin-project/go-filecoin/repo"
	"github.com/filecoin-project/go-filecoin/util/moresync"
	"github.com/filecoin-project/go-filecoin/version"
	"github.com/filecoin-project/go-filecoin/wallet"
	bserv "github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-hamt-ipld"
	bstore "github.com/ipfs/go-ipfs-blockstore"
	exchange "github.com/ipfs/go-ipfs-exchange-interface"
)

// BlockMiningSubmodule enhances the `Node` with block mining capabilities.
//
// TODO: complete the refactor https://github.com/filecoin-project/go-filecoin/issues/3140
type BlockMiningSubmodule struct {
	BlockMiningAPI *block.MiningAPI

	// Mining stuff.
	AddNewlyMinedBlock newBlockFunc
	// cancelMining cancels the context for block production and sector commitments.
	cancelMining    context.CancelFunc
	MiningWorker    mining.Worker
	MiningScheduler mining.Scheduler
	mining          struct {
		sync.Mutex
		isMining bool
	}
	miningDoneWg *sync.WaitGroup
}

// ChainSubmodule enhances the `Node` with chain capabilities.
type ChainSubmodule struct {
	NetworkName  string
	Consensus    consensus.Protocol
	ChainReader  nodeChainReader
	MessageStore *chain.MessageStore
	Syncer       nodeChainSyncer
	PowerTable   consensus.PowerTableView
	// HeavyTipSetCh is a subscription to the heaviest tipset topic on the chain.
	// https://github.com/filecoin-project/go-filecoin/issues/2309
	HeaviestTipSetCh chan interface{}
	// cancelChainSync cancels the context for chain sync subscriptions and handlers.
	cancelChainSync context.CancelFunc
	// ChainSynced is a latch that releases when a nodes chain reaches a caught-up state.
	// It serves as a barrier to be released when the initial chain sync has completed.
	// Services which depend on a more-or-less synced chain can wait for this before starting up.
	ChainSynced *moresync.Latch
}

// StorageProtocolSubmodule enhances the `Node` with "Storage" protocol capabilities.
type StorageProtocolSubmodule struct {
	StorageAPI *storage.API

	// Storage Market Interfaces
	StorageMiner *storage.Miner
}

// RetrievalProtocolSubmodule enhances the `Node` with "Retrieval" protocol capabilities.
type RetrievalProtocolSubmodule struct {
	RetrievalAPI *retrieval.API

	// Retrieval Interfaces
	RetrievalMiner *retrieval.Miner
}

// SectorBuilderSubmodule enhances the `Node` with sector storage capabilities.
type SectorBuilderSubmodule struct {
	// SectorBuilder is used by the miner to fill and seal sectors.
	sectorBuilder sectorbuilder.SectorBuilder
}

// ToSplitOrNotToSplitNode is part of an ongoing refactor to cleanup `node.Node`.
//
// TODO: complete the refactor https://github.com/filecoin-project/go-filecoin/issues/3140
type ToSplitOrNotToSplitNode struct {
	VersionTable version.ProtocolVersionTable

	PorcelainAPI *porcelain.API

	// Review: is this message queue only used for block mining?
	// Incoming messages for block mining.
	Inbox *message.Inbox
	// Messages sent and not yet mined.
	Outbox *message.Outbox

	Wallet *wallet.Wallet

	StorageFaultSlasher storageFaultSlasher

	// Data Storage Fields

	// Repo is the repo this node was created with
	// it contains all persistent artifacts of the filecoin node
	Repo repo.Repo

	// TODO: network networking
	HelloSvc     *hello.Handler
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
