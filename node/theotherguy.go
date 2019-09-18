package node

import (
	"context"
	"sync"

	"github.com/filecoin-project/go-filecoin/chain"
	"github.com/filecoin-project/go-filecoin/consensus"
	"github.com/filecoin-project/go-filecoin/message"
	"github.com/filecoin-project/go-filecoin/mining"
	"github.com/filecoin-project/go-filecoin/porcelain"
	"github.com/filecoin-project/go-filecoin/proofs/sectorbuilder"
	"github.com/filecoin-project/go-filecoin/protocol/block"
	"github.com/filecoin-project/go-filecoin/protocol/retrieval"
	"github.com/filecoin-project/go-filecoin/protocol/storage"
	"github.com/filecoin-project/go-filecoin/repo"
	"github.com/filecoin-project/go-filecoin/util/moresync"
	"github.com/filecoin-project/go-filecoin/version"
	"github.com/filecoin-project/go-filecoin/wallet"
)

// ToSplitOrNotToSplitNode is part of an ongoing refactor to cleanup `node.Node`.
//
// TODO: complete the refactor https://github.com/filecoin-project/go-filecoin/issues/3140
type ToSplitOrNotToSplitNode struct {
	Consensus    consensus.Protocol
	ChainReader  nodeChainReader
	MessageStore *chain.MessageStore
	Syncer       nodeChainSyncer
	PowerTable   consensus.PowerTableView
	NetworkName  string
	VersionTable version.ProtocolVersionTable

	BlockMiningAPI *block.MiningAPI
	PorcelainAPI   *porcelain.API
	RetrievalAPI   *retrieval.API
	StorageAPI     *storage.API

	// HeavyTipSetCh is a subscription to the heaviest tipset topic on the chain.
	// https://github.com/filecoin-project/go-filecoin/issues/2309
	HeaviestTipSetCh chan interface{}
	// cancelChainSync cancels the context for chain sync subscriptions and handlers.
	cancelChainSync context.CancelFunc

	// Incoming messages for block mining.
	Inbox *message.Inbox
	// Messages sent and not yet mined.
	Outbox *message.Outbox

	Wallet *wallet.Wallet

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

	// Storage Market Interfaces
	StorageMiner *storage.Miner

	StorageFaultSlasher storageFaultSlasher

	// Retrieval Interfaces
	RetrievalMiner *retrieval.Miner

	// Data Storage Fields

	// Repo is the repo this node was created with
	// it contains all persistent artifacts of the filecoin node
	Repo repo.Repo

	// SectorBuilder is used by the miner to fill and seal sectors.
	sectorBuilder sectorbuilder.SectorBuilder

	// ChainSynced is a latch that releases when a nodes chain reaches a caught-up state.
	// It serves as a barrier to be released when the initial chain sync has completed.
	// Services which depend on a more-or-less synced chain can wait for this before starting up.
	ChainSynced *moresync.Latch
}
