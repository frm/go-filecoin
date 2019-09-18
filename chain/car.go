package chain

import (
	"context"
	"io"

	car "github.com/ipfs/go-car"
	carutil "github.com/ipfs/go-car/util"
	"github.com/ipfs/go-ipfs-blockstore"
	cbor "github.com/ipfs/go-ipld-cbor"

	"github.com/filecoin-project/go-filecoin/types"
)

type carChainReader interface {
	GetHead() types.TipSetKey
	GetTipSet(types.TipSetKey) (types.TipSet, error)
}
type carMessageReader interface {
	MessageProvider
}

// Export will export a chain (all blocks and their messages) to the writer `out`.
func Export(ctx context.Context, cr carChainReader, mr carMessageReader, out io.Writer) error {
	headTS, err := cr.GetTipSet(cr.GetHead())
	if err != nil {
		return err
	}
	// Write the car header
	chb, err := cbor.DumpObject(car.CarHeader{
		Roots:   headTS.Key().ToSlice(),
		Version: 1,
	})
	if err != nil {
		return err
	}
	if err := carutil.LdWrite(out, chb); err != nil {
		return err
	}

	iter := IterAncestors(ctx, cr, headTS)
	// Accumulate TipSets in descending order.
	for ; !iter.Complete(); err = iter.Next() {
		if err != nil {
			return err
		}
		tip := iter.Value()
		// write block
		for i := 0; i < tip.Len(); i++ {
			hdr := tip.At(i)
			if err := carutil.LdWrite(out, hdr.Cid().Bytes(), hdr.ToNode().RawData()); err != nil {
				return err
			}

			msgs, err := mr.LoadMessages(ctx, hdr.Messages)
			if err != nil {
				return err
			}

			// write messages, will contain duplicate messages shared by blocks
			for _, msg := range msgs {
				c, err := msg.Cid()
				if err != nil {
					return err
				}
				n, err := msg.ToNode()
				if err != nil {
					return err
				}
				if err := carutil.LdWrite(out, c.Bytes(), n.RawData()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func Import(ctx context.Context, bs blockstore.Blockstore, in io.Reader) (*car.CarHeader, error) {
	return car.LoadCar(bs, in)
}
