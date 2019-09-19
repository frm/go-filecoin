package node

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"time"

	bserv "github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-hamt-ipld"
	bstore "github.com/ipfs/go-ipfs-blockstore"
	logging "github.com/ipfs/go-log"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/routing"
	"github.com/pkg/errors"

	"github.com/filecoin-project/go-filecoin/address"
	"github.com/filecoin-project/go-filecoin/chain"
	"github.com/filecoin-project/go-filecoin/clock"
	"github.com/filecoin-project/go-filecoin/config"
	"github.com/filecoin-project/go-filecoin/consensus"
	"github.com/filecoin-project/go-filecoin/message"
	"github.com/filecoin-project/go-filecoin/metrics"
	"github.com/filecoin-project/go-filecoin/mining"
	"github.com/filecoin-project/go-filecoin/net"
	"github.com/filecoin-project/go-filecoin/net/pubsub"
	"github.com/filecoin-project/go-filecoin/paths"
	"github.com/filecoin-project/go-filecoin/proofs/sectorbuilder"
	"github.com/filecoin-project/go-filecoin/protocol/block"
	"github.com/filecoin-project/go-filecoin/protocol/hello"
	"github.com/filecoin-project/go-filecoin/protocol/retrieval"
	"github.com/filecoin-project/go-filecoin/protocol/storage"
	"github.com/filecoin-project/go-filecoin/state"
	"github.com/filecoin-project/go-filecoin/types"
	"github.com/filecoin-project/go-filecoin/vm"
	vmerr "github.com/filecoin-project/go-filecoin/vm/errors"
)

var log = logging.Logger("node") // nolint: deadcode

var (
	// ErrNoMinerAddress is returned when the node is not configured to have any miner addresses.
	ErrNoMinerAddress = errors.New("no miner addresses configured")
)

// Node represents a full Filecoin node.
//
// TODO: remove all the 3140's from names before merging https://github.com/filecoin-project/go-filecoin/issues/3140
type Node struct {
	host     host.Host
	PeerHost host.Host

	// Router is a router from IPFS
	Router routing.Routing

	// Network Fields
	BlockSub   pubsub.Subscription
	MessageSub pubsub.Subscription

	// OfflineMode, when true, disables libp2p
	OfflineMode bool

	// Clock is a clock used by the node for time.
	Clock clock.Clock

	Refactor3140 ToSplitOrNotToSplitNode

	Chain3140 ChainSubmodule

	BlockMining3140 BlockMiningSubmodule

	StorageProtocol3140 StorageProtocolSubmodule

	RetrievalProtocol3140 RetrievalProtocolSubmodule

	SectorBuilder3140 SectorBuilderSubmodule
}

// Start boots up the node.
func (node *Node) Start(ctx context.Context) error {
	if err := metrics.RegisterPrometheusEndpoint(node.Refactor3140.Repo.Config().Observability.Metrics); err != nil {
		return errors.Wrap(err, "failed to setup metrics")
	}

	if err := metrics.RegisterJaeger(node.host.ID().Pretty(), node.Refactor3140.Repo.Config().Observability.Tracing); err != nil {
		return errors.Wrap(err, "failed to setup tracing")
	}

	var err error
	if err = node.Chain3140.ChainReader.Load(ctx); err != nil {
		return err
	}

	// Only set these up if there is a miner configured.
	if _, err := node.MiningAddress(); err == nil {
		if err := node.setupMining(ctx); err != nil {
			log.Errorf("setup mining failed: %v", err)
			return err
		}
	}

	// TODO: defer establishing these API endpoints until the chain is synced when the commands
	//   can handle their absence: https://github.com/filecoin-project/go-filecoin/issues/3137
	err = node.setupProtocols()
	if err != nil {
		return errors.Wrap(err, "failed to set up protocols:")
	}
	node.RetrievalProtocol3140.RetrievalMiner = retrieval.NewMiner(node)

	var syncCtx context.Context
	syncCtx, node.Chain3140.cancelChainSync = context.WithCancel(context.Background())

	// Wire up propagation of new chain heads from the chain store to other components.
	head, err := node.Refactor3140.PorcelainAPI.ChainHead()
	if err != nil {
		return errors.Wrap(err, "failed to get chain head")
	}
	go node.handleNewChainHeads(syncCtx, head)

	if !node.OfflineMode {
		// Start bootstrapper.
		node.Refactor3140.Bootstrapper.Start(context.Background())

		// Register peer tracker disconnect function with network.
		net.TrackerRegisterDisconnect(node.host.Network(), node.Refactor3140.PeerTracker)

		// Start up 'hello' handshake service
		helloCallback := func(ci *types.ChainInfo) {
			node.Refactor3140.PeerTracker.Track(ci)
			// TODO Implement principled trusting of ChainInfo's
			// to address in #2674
			trusted := true
			err := node.Chain3140.Syncer.HandleNewTipSet(context.Background(), ci, trusted)
			if err != nil {
				log.Infof("error handling tipset from hello %s: %s", ci, err)
				return
			}
			// For now, consider the initial bootstrap done after the syncer has (synchronously)
			// processed the chain up to the head reported by the first peer to respond to hello.
			// This is an interim sequence until a secure network bootstrap is implemented:
			// https://github.com/filecoin-project/go-filecoin/issues/2674.
			// For now, we trust that the first node to respond will be a configured bootstrap node
			// and that we trust that node to inform us of the chain head.
			// TODO: when the syncer rejects too-far-ahead blocks received over pubsub, don't consider
			// sync done until it's caught up enough that it will accept blocks from pubsub.
			// This might require additional rounds of hello.
			// See https://github.com/filecoin-project/go-filecoin/issues/1105
			node.Chain3140.ChainSynced.Done()
		}
		node.Refactor3140.HelloSvc = hello.New(node.Host(), node.Chain3140.ChainReader.GenesisCid(), helloCallback, node.Refactor3140.PorcelainAPI.ChainHead, node.Chain3140.NetworkName)

		// register the update function on the peer tracker now that we have a hello service
		node.Refactor3140.PeerTracker.SetUpdateFn(func(ctx context.Context, p peer.ID) (*types.ChainInfo, error) {
			hmsg, err := node.Refactor3140.HelloSvc.ReceiveHello(ctx, p)
			if err != nil {
				return nil, err
			}
			return types.NewChainInfo(p, hmsg.HeaviestTipSetCids, hmsg.HeaviestTipSetHeight), nil
		})

		// Subscribe to block pubsub after the initial sync completes.
		go func() {
			node.Chain3140.ChainSynced.Wait()

			// Log some information about the synced chain
			if ts, err := node.Chain3140.ChainReader.GetTipSet(node.Chain3140.ChainReader.GetHead()); err == nil {
				if height, err := ts.Height(); err == nil {
					log.Infof("initial chain sync complete! chain head height %d, tipset key %s, blocks %s\n", height, ts.Key(), ts.String())
				}
			}

			if syncCtx.Err() == nil {
				// Subscribe to block pubsub topic to learn about new chain heads.
				node.BlockSub, err = node.pubsubscribe(syncCtx, net.BlockTopic(node.Chain3140.NetworkName), node.processBlock)
				if err != nil {
					log.Error(err)
				}
			}
		}()

		// Subscribe to the message pubsub topic to learn about messages to mine into blocks.
		// TODO: defer this subscription until after mining (block production) is started:
		// https://github.com/filecoin-project/go-filecoin/issues/2145.
		// This is blocked by https://github.com/filecoin-project/go-filecoin/issues/2959, which
		// is necessary for message_propagate_test to start mining before testing this behaviour.
		node.MessageSub, err = node.pubsubscribe(syncCtx, net.MessageTopic(node.Chain3140.NetworkName), node.processMessage)
		if err != nil {
			return err
		}

		// Start heartbeats.
		if err := node.setupHeartbeatServices(ctx); err != nil {
			return errors.Wrap(err, "failed to start heartbeat services")
		}
	}

	return nil
}

// Subscribes a handler function to a pubsub topic.
func (node *Node) pubsubscribe(ctx context.Context, topic string, handler pubSubHandler) (pubsub.Subscription, error) {
	sub, err := node.Refactor3140.PorcelainAPI.PubSubSubscribe(topic)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to subscribe to %s", topic)
	}
	go node.handleSubscription(ctx, sub, handler)
	return sub, nil
}

func (node *Node) setupHeartbeatServices(ctx context.Context) error {
	mag := func() address.Address {
		addr, err := node.MiningAddress()
		// the only error MiningAddress() returns is ErrNoMinerAddress.
		// if there is no configured miner address, simply send a zero
		// address across the wire.
		if err != nil {
			return address.Undef
		}
		return addr
	}

	// start the primary heartbeat service
	if len(node.Refactor3140.Repo.Config().Heartbeat.BeatTarget) > 0 {
		hbs := metrics.NewHeartbeatService(node.Host(), node.Chain3140.ChainReader.GenesisCid(), node.Refactor3140.Repo.Config().Heartbeat, node.Refactor3140.PorcelainAPI.ChainHead, metrics.WithMinerAddressGetter(mag))
		go hbs.Start(ctx)
	}

	// check if we want to connect to an alert service. An alerting service is a heartbeat
	// service that can trigger alerts based on the contents of heatbeats.
	if alertTarget := os.Getenv("FIL_HEARTBEAT_ALERTS"); len(alertTarget) > 0 {
		ahbs := metrics.NewHeartbeatService(node.Host(), node.Chain3140.ChainReader.GenesisCid(), &config.HeartbeatConfig{
			BeatTarget:      alertTarget,
			BeatPeriod:      "10s",
			ReconnectPeriod: "10s",
			Nickname:        node.Refactor3140.Repo.Config().Heartbeat.Nickname,
		}, node.Refactor3140.PorcelainAPI.ChainHead, metrics.WithMinerAddressGetter(mag))
		go ahbs.Start(ctx)
	}
	return nil
}

func (node *Node) setupMining(ctx context.Context) error {
	// initialize a sector builder
	sectorBuilder, err := initSectorBuilderForNode(ctx, node)
	if err != nil {
		return errors.Wrap(err, "failed to initialize sector builder")
	}
	node.SectorBuilder3140.sectorBuilder = sectorBuilder

	return nil
}

func (node *Node) setIsMining(isMining bool) {
	node.BlockMining3140.mining.Lock()
	defer node.BlockMining3140.mining.Unlock()
	node.BlockMining3140.mining.isMining = isMining
}

func (node *Node) handleNewMiningOutput(ctx context.Context, miningOutCh <-chan mining.Output) {
	defer func() {
		node.BlockMining3140.miningDoneWg.Done()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case output, ok := <-miningOutCh:
			if !ok {
				return
			}
			if output.Err != nil {
				log.Errorf("stopping mining. error: %s", output.Err.Error())
				node.StopMining(context.Background())
			} else {
				node.BlockMining3140.miningDoneWg.Add(1)
				go func() {
					if node.IsMining() {
						node.BlockMining3140.AddNewlyMinedBlock(ctx, output.NewBlock)
					}
					node.BlockMining3140.miningDoneWg.Done()
				}()
			}
		}
	}

}

func (node *Node) handleNewChainHeads(ctx context.Context, prevHead types.TipSet) {
	node.Chain3140.HeaviestTipSetCh = node.Chain3140.ChainReader.HeadEvents().Sub(chain.NewHeadTopic)
	handler := message.NewHeadHandler(node.Refactor3140.Inbox, node.Refactor3140.Outbox, node.Chain3140.ChainReader, prevHead)

	for {
		select {
		case ts, ok := <-node.Chain3140.HeaviestTipSetCh:
			if !ok {
				return
			}
			newHead, ok := ts.(types.TipSet)
			if !ok {
				log.Warning("non-tipset published on heaviest tipset channel")
				continue
			}

			if err := handler.HandleNewHead(ctx, newHead); err != nil {
				log.Error(err)
			}

			if node.StorageProtocol3140.StorageMiner != nil {
				if _, err := node.StorageProtocol3140.StorageMiner.OnNewHeaviestTipSet(newHead); err != nil {
					log.Error(err)
				}
			}
			if node.Refactor3140.StorageFaultSlasher != nil {
				if err := node.Refactor3140.StorageFaultSlasher.OnNewHeaviestTipSet(ctx, newHead); err != nil {
					log.Error(err)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (node *Node) cancelSubscriptions() {
	if node.Chain3140.cancelChainSync != nil {
		node.Chain3140.cancelChainSync()
	}

	if node.BlockSub != nil {
		node.BlockSub.Cancel()
		node.BlockSub = nil
	}

	if node.MessageSub != nil {
		node.MessageSub.Cancel()
		node.MessageSub = nil
	}
}

// Stop initiates the shutdown of the node.
func (node *Node) Stop(ctx context.Context) {
	node.Chain3140.ChainReader.HeadEvents().Unsub(node.Chain3140.HeaviestTipSetCh)
	node.StopMining(ctx)

	node.cancelSubscriptions()
	node.Chain3140.ChainReader.Stop()

	if node.SectorBuilder() != nil {
		if err := node.SectorBuilder().Close(); err != nil {
			fmt.Printf("error closing sector builder: %s\n", err)
		}
		node.SectorBuilder3140.sectorBuilder = nil
	}

	if err := node.Host().Close(); err != nil {
		fmt.Printf("error closing host: %s\n", err)
	}

	if err := node.Refactor3140.Repo.Close(); err != nil {
		fmt.Printf("error closing repo: %s\n", err)
	}

	node.Refactor3140.Bootstrapper.Stop()

	fmt.Println("stopping filecoin :(")
}

type newBlockFunc func(context.Context, *types.Block)

func (node *Node) addNewlyMinedBlock(ctx context.Context, b *types.Block) {
	log.Debugf("Got a newly mined block from the mining worker: %s", b)
	if err := node.AddNewBlock(ctx, b); err != nil {
		log.Warningf("error adding new mined block: %s. err: %s", b.Cid().String(), err.Error())
	}
}

// MiningAddress returns the address of the mining actor mining on behalf of
// the node.
func (node *Node) MiningAddress() (address.Address, error) {
	addr := node.Refactor3140.Repo.Config().Mining.MinerAddress
	if addr.Empty() {
		return address.Undef, ErrNoMinerAddress
	}

	return addr, nil
}

// MiningTimes returns the configured time it takes to mine a block, and also
// the mining delay duration, which is currently a fixed fraction of block time.
// Note this is mocked behavior, in production this time is determined by how
// long it takes to generate PoSTs.
func (node *Node) MiningTimes() (time.Duration, time.Duration) {
	blockTime := node.Refactor3140.PorcelainAPI.BlockTime()
	mineDelay := blockTime / mining.MineDelayConversionFactor
	return blockTime, mineDelay
}

// SetupMining initializes all the functionality the node needs to start mining.
// This method is idempotent.
func (node *Node) SetupMining(ctx context.Context) error {
	// ensure we have a miner actor before we even consider mining
	minerAddr, err := node.MiningAddress()
	if err != nil {
		return errors.Wrap(err, "failed to get mining address")
	}
	_, err = node.Refactor3140.PorcelainAPI.ActorGet(ctx, minerAddr)
	if err != nil {
		return errors.Wrap(err, "failed to get miner actor")
	}

	// ensure we have a sector builder
	if node.SectorBuilder() == nil {
		if err := node.setupMining(ctx); err != nil {
			return err
		}
	}

	// ensure we have a mining worker
	if node.BlockMining3140.MiningWorker == nil {
		if node.BlockMining3140.MiningWorker, err = node.CreateMiningWorker(ctx); err != nil {
			return err
		}
	}

	// ensure we have a storage miner
	if node.StorageProtocol3140.StorageMiner == nil {
		storageMiner, _, err := initStorageMinerForNode(ctx, node)
		if err != nil {
			return errors.Wrap(err, "failed to initialize storage miner")
		}
		node.StorageProtocol3140.StorageMiner = storageMiner
	}

	return nil
}

// StartMining causes the node to start feeding blocks to the mining worker and initializes
// the SectorBuilder for the mining address.
func (node *Node) StartMining(ctx context.Context) error {
	if node.IsMining() {
		return errors.New("Node is already mining")
	}

	err := node.SetupMining(ctx)
	if err != nil {
		return err
	}

	minerAddr, err := node.MiningAddress()
	if err != nil {
		return errors.Wrap(err, "failed to get mining address")
	}

	minerOwnerAddr, err := node.Refactor3140.PorcelainAPI.MinerGetOwnerAddress(ctx, minerAddr)
	if err != nil {
		return errors.Wrapf(err, "failed to get mining owner address for miner %s", minerAddr)
	}

	_, mineDelay := node.MiningTimes()

	if node.BlockMining3140.MiningScheduler == nil {
		node.BlockMining3140.MiningScheduler = mining.NewScheduler(node.BlockMining3140.MiningWorker, mineDelay, node.Refactor3140.PorcelainAPI.ChainHead)
	} else if node.BlockMining3140.MiningScheduler.IsStarted() {
		return fmt.Errorf("miner scheduler already started")
	}

	var miningCtx context.Context
	miningCtx, node.BlockMining3140.cancelMining = context.WithCancel(context.Background())

	outCh, doneWg := node.BlockMining3140.MiningScheduler.Start(miningCtx)

	node.BlockMining3140.miningDoneWg = doneWg
	node.BlockMining3140.AddNewlyMinedBlock = node.addNewlyMinedBlock
	node.BlockMining3140.miningDoneWg.Add(1)
	go node.handleNewMiningOutput(miningCtx, outCh)

	// initialize the storage fault slasher
	node.Refactor3140.StorageFaultSlasher = storage.NewFaultSlasher(
		node.Refactor3140.PorcelainAPI,
		node.Refactor3140.Outbox,
		storage.DefaultFaultSlasherGasPrice,
		storage.DefaultFaultSlasherGasLimit)

	// loop, turning sealing-results into commitSector messages to be included
	// in the chain
	go func() {
		for {
			select {
			case result := <-node.SectorBuilder().SectorSealResults():
				if result.SealingErr != nil {
					log.Errorf("failed to seal sector with id %d: %s", result.SectorID, result.SealingErr.Error())
				} else if result.SealingResult != nil {

					// TODO: determine these algorithmically by simulating call and querying historical prices
					gasPrice := types.NewGasPrice(1)
					gasUnits := types.NewGasUnits(300)

					val := result.SealingResult

					// look up miner worker address. If this fails, something is really wrong
					// so we bail and don't commit sectors.
					workerAddr, err := node.Refactor3140.PorcelainAPI.MinerGetWorkerAddress(miningCtx, minerAddr, node.Chain3140.ChainReader.GetHead())
					if err != nil {
						log.Errorf("failed to get worker address %s", err)
						continue
					}

					// This call can fail due to, e.g. nonce collisions. Our miners existence depends on this.
					// We should deal with this, but MessageSendWithRetry is problematic.
					msgCid, err := node.Refactor3140.PorcelainAPI.MessageSend(
						miningCtx,
						workerAddr,
						minerAddr,
						types.ZeroAttoFIL,
						gasPrice,
						gasUnits,
						"commitSector",
						val.SectorID,
						val.CommD[:],
						val.CommR[:],
						val.CommRStar[:],
						val.Proof[:],
					)

					if err != nil {
						log.Errorf("failed to send commitSector message from %s to %s for sector with id %d: %s", minerOwnerAddr, minerAddr, val.SectorID, err)
						continue
					}

					node.StorageProtocol3140.StorageMiner.OnCommitmentSent(val, msgCid, nil)
				}
			case <-miningCtx.Done():
				return
			}
		}
	}()

	// schedules sealing of staged piece-data
	if node.Refactor3140.Repo.Config().Mining.AutoSealIntervalSeconds > 0 {
		go func() {
			for {
				select {
				case <-miningCtx.Done():
					return
				case <-time.After(time.Duration(node.Refactor3140.Repo.Config().Mining.AutoSealIntervalSeconds) * time.Second):
					log.Info("auto-seal has been triggered")
					if err := node.SectorBuilder().SealAllStagedSectors(miningCtx); err != nil {
						log.Errorf("scheduler received error from node.SectorBuilder3140.sectorBuilder.SealAllStagedSectors (%s) - exiting", err.Error())
						return
					}
				}
			}
		}()
	} else {
		log.Debug("auto-seal is disabled")
	}
	node.setIsMining(true)

	return nil
}

// NetworkNameFromGenesis retrieves the name of the current network from the genesis block.
// The network name can not change while this node is running. Since the network name determines
// the protocol version, we must retrieve it at genesis where the protocol is known.
func networkNameFromGenesis(ctx context.Context, chainStore *chain.Store, bs bstore.Blockstore) (string, error) {
	st, err := chainStore.GetGenesisState(ctx)
	if err != nil {
		return "", errors.Wrap(err, "could not get genesis state")
	}

	vms := vm.NewStorageMap(bs)
	res, _, err := consensus.CallQueryMethod(ctx, st, vms, address.InitAddress, "getNetwork", nil, address.Undef, types.NewBlockHeight(0))
	if err != nil {
		return "", errors.Wrap(err, "error querying for network name")
	}

	return string(res[0]), nil
}

func initSectorBuilderForNode(ctx context.Context, node *Node) (sectorbuilder.SectorBuilder, error) {
	minerAddr, err := node.MiningAddress()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get node's mining address")
	}

	sectorSize, err := node.Refactor3140.PorcelainAPI.MinerGetSectorSize(ctx, minerAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get sector size for miner w/address %s", minerAddr.String())
	}

	lastUsedSectorID, err := node.Refactor3140.PorcelainAPI.MinerGetLastCommittedSectorID(ctx, minerAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get last used sector id for miner w/address %s", minerAddr.String())
	}

	// TODO: Currently, weconfigure the RustSectorBuilder to store its
	// metadata in the staging directory, it should be in its own directory.
	//
	// Tracked here: https://github.com/filecoin-project/rust-fil-proofs/issues/402
	repoPath, err := node.Refactor3140.Repo.Path()
	if err != nil {
		return nil, err
	}
	sectorDir, err := paths.GetSectorPath(node.Refactor3140.Repo.Config().SectorBase.RootDir, repoPath)
	if err != nil {
		return nil, err
	}

	stagingDir, err := paths.StagingDir(sectorDir)
	if err != nil {
		return nil, err
	}

	sealedDir, err := paths.SealedDir(sectorDir)
	if err != nil {
		return nil, err
	}
	cfg := sectorbuilder.RustSectorBuilderConfig{
		BlockService:     node.Refactor3140.blockservice,
		LastUsedSectorID: lastUsedSectorID,
		MetadataDir:      stagingDir,
		MinerAddr:        minerAddr,
		SealedSectorDir:  sealedDir,
		StagedSectorDir:  stagingDir,
		SectorClass:      types.NewSectorClass(sectorSize),
	}

	sb, err := sectorbuilder.NewRustSectorBuilder(cfg)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to initialize sector builder for miner %s", minerAddr.String()))
	}

	return sb, nil
}

// initStorageMinerForNode initializes the storage miner, returning the miner, the miner owner address (to be
// passed to storage fault slasher) and any error
func initStorageMinerForNode(ctx context.Context, node *Node) (*storage.Miner, address.Address, error) {
	minerAddr, err := node.MiningAddress()
	if err != nil {
		return nil, address.Undef, errors.Wrap(err, "failed to get node's mining address")
	}

	ownerAddress, err := node.Refactor3140.PorcelainAPI.MinerGetOwnerAddress(ctx, minerAddr)
	if err != nil {
		return nil, address.Undef, errors.Wrap(err, "no mining owner available, skipping storage miner setup")
	}

	workerAddress, err := node.Refactor3140.PorcelainAPI.MinerGetWorkerAddress(ctx, minerAddr, node.Chain3140.ChainReader.GetHead())
	if err != nil {
		return nil, address.Undef, errors.Wrap(err, "failed to fetch miner's worker address")
	}

	sectorSize, err := node.Refactor3140.PorcelainAPI.MinerGetSectorSize(ctx, minerAddr)
	if err != nil {
		return nil, address.Undef, errors.Wrap(err, "failed to fetch miner's sector size")
	}

	prover := storage.NewProver(minerAddr, sectorSize, node.Refactor3140.PorcelainAPI, node.Refactor3140.PorcelainAPI)

	miner, err := storage.NewMiner(
		minerAddr,
		ownerAddress,
		prover,
		sectorSize,
		node,
		node.Refactor3140.Repo.DealsDatastore(),
		node.Refactor3140.PorcelainAPI)
	if err != nil {
		return nil, address.Undef, errors.Wrap(err, "failed to instantiate storage miner")
	}

	return miner, workerAddress, nil
}

// StopMining stops mining on new blocks.
func (node *Node) StopMining(ctx context.Context) {
	node.setIsMining(false)

	if node.BlockMining3140.cancelMining != nil {
		node.BlockMining3140.cancelMining()
	}

	if node.BlockMining3140.miningDoneWg != nil {
		node.BlockMining3140.miningDoneWg.Wait()
	}

	// TODO: stop node.StorageProtocol3140.StorageMiner
}

func (node *Node) handleSubscription(ctx context.Context, sub pubsub.Subscription, handler pubSubHandler) {
	for {
		received, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != context.Canceled {
				log.Errorf("error reading message from topic %s: %s", sub.Topic(), err)
			}
			return
		}

		if err := handler(ctx, received); err != nil {
			handlerName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
			if vmerr.ShouldRevert(err) {
				log.Infof("error in handler %s for topic %s: %s", handlerName, sub.Topic(), err)
			} else if err != context.Canceled {
				log.Errorf("error in handler %s for topic %s: %s", handlerName, sub.Topic(), err)
			}
		}
	}
}

// setupProtocols creates protocol clients and miners, then sets the node's APIs
// for each
func (node *Node) setupProtocols() error {
	_, mineDelay := node.MiningTimes()
	blockMiningAPI := block.New(
		node.MiningAddress,
		node.AddNewBlock,
		node.Chain3140.ChainReader,
		node.IsMining,
		mineDelay,
		node.SetupMining,
		node.StartMining,
		node.StopMining,
		node.GetMiningWorker)

	node.BlockMining3140.BlockMiningAPI = &blockMiningAPI

	// set up retrieval client and api
	retapi := retrieval.NewAPI(retrieval.NewClient(node.host, node.Refactor3140.PorcelainAPI))
	node.RetrievalProtocol3140.RetrievalAPI = &retapi

	// set up storage client and api
	smc := storage.NewClient(node.host, node.Refactor3140.PorcelainAPI)
	smcAPI := storage.NewAPI(smc)
	node.StorageProtocol3140.StorageAPI = &smcAPI
	return nil
}

// GetMiningWorker ensures mining is setup and then returns the worker
func (node *Node) GetMiningWorker(ctx context.Context) (mining.Worker, error) {
	if err := node.SetupMining(ctx); err != nil {
		return nil, err
	}
	return node.BlockMining3140.MiningWorker, nil
}

// CreateMiningWorker creates a mining.Worker for the node using the configured
// getStateTree, getWeight, and getAncestors functions for the node
func (node *Node) CreateMiningWorker(ctx context.Context) (mining.Worker, error) {
	processor := consensus.NewDefaultProcessor()

	minerAddr, err := node.MiningAddress()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mining address")
	}

	minerOwnerAddr, err := node.Refactor3140.PorcelainAPI.MinerGetOwnerAddress(ctx, minerAddr)
	if err != nil {
		log.Errorf("could not get owner address of miner actor")
		return nil, err
	}
	return mining.NewDefaultWorker(mining.WorkerParameters{
		API: node.Refactor3140.PorcelainAPI,

		MinerAddr:      minerAddr,
		MinerOwnerAddr: minerOwnerAddr,
		WorkerSigner:   node.Refactor3140.Wallet,

		GetStateTree: node.getStateTree,
		GetWeight:    node.getWeight,
		GetAncestors: node.getAncestors,
		Election:     consensus.ElectionMachine{},
		TicketGen:    consensus.TicketMachine{},

		MessageSource: node.Refactor3140.Inbox.Pool(),
		MessageStore:  node.Chain3140.MessageStore,
		Processor:     processor,
		PowerTable:    node.Chain3140.PowerTable,
		Blockstore:    node.Refactor3140.Blockstore,
		Clock:         node.Clock,
	}), nil
}

// getStateTree is the default GetStateTree function for the mining worker.
func (node *Node) getStateTree(ctx context.Context, ts types.TipSet) (state.Tree, error) {
	return node.Chain3140.ChainReader.GetTipSetState(ctx, ts.Key())
}

// getWeight is the default GetWeight function for the mining worker.
func (node *Node) getWeight(ctx context.Context, ts types.TipSet) (uint64, error) {
	parent, err := ts.Parents()
	if err != nil {
		return uint64(0), err
	}
	// TODO handle genesis cid more gracefully
	if parent.Len() == 0 {
		return node.Chain3140.Consensus.Weight(ctx, ts, nil)
	}
	pSt, err := node.Chain3140.ChainReader.GetTipSetState(ctx, parent)
	if err != nil {
		return uint64(0), err
	}
	return node.Chain3140.Consensus.Weight(ctx, ts, pSt)
}

// getAncestors is the default GetAncestors function for the mining worker.
func (node *Node) getAncestors(ctx context.Context, ts types.TipSet, newBlockHeight *types.BlockHeight) ([]types.TipSet, error) {
	ancestorHeight := newBlockHeight.Sub(types.NewBlockHeight(consensus.AncestorRoundsNeeded))
	return chain.GetRecentAncestors(ctx, ts, node.Chain3140.ChainReader, ancestorHeight)
}

// -- Accessors

// Host returns the nodes host.
func (node *Node) Host() host.Host {
	return node.host
}

// SectorBuilder returns the nodes sectorBuilder.
func (node *Node) SectorBuilder() sectorbuilder.SectorBuilder {
	return node.SectorBuilder3140.sectorBuilder
}

// BlockService returns the nodes blockservice.
func (node *Node) BlockService() bserv.BlockService {
	return node.Refactor3140.blockservice
}

// CborStore returns the nodes cborStore.
func (node *Node) CborStore() *hamt.CborIpldStore {
	return node.Refactor3140.cborStore
}

// IsMining returns a boolean indicating whether the node is mining blocks.
func (node *Node) IsMining() bool {
	node.BlockMining3140.mining.Lock()
	defer node.BlockMining3140.mining.Unlock()
	return node.BlockMining3140.mining.isMining
}
