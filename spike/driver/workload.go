package driver

import "spike/book"

type XorShift64 struct {
	state uint64
}

func NewXorShift64(seed uint64) *XorShift64 {
	if seed == 0 {
		seed = 0x853c49e6748fea9b
	}
	return &XorShift64{state: seed}
}

func (r *XorShift64) Next() uint64 {
	x := r.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.state = x
	return x
}

func (r *XorShift64) NextN(n uint64) uint64 {
	return r.Next() % n
}

type WorkloadGenerator struct {
	rng            *XorShift64
	nextOrderID    uint64
	activeOrderIDs []uint64
}

func NewWorkloadGenerator(seed uint64) *WorkloadGenerator {
	return &WorkloadGenerator{
		rng:            NewXorShift64(seed),
		nextOrderID:    1,
		activeOrderIDs: make([]uint64, 0, book.MaxCapacity),
	}
}

// NextCommand produces the next command in the mix:
// 50% New Limit, 30% Cancel, 10% ReplaceDown, 10% MarketCross.
func (g *WorkloadGenerator) NextCommand() book.Command {
	roll := g.rng.NextN(100)
	side := book.SideBid
	if g.rng.NextN(2) == 1 {
		side = book.SideAsk
	}

	switch {
	case roll < 50 || len(g.activeOrderIDs) == 0:
		// New Limit Order
		id := g.nextOrderID
		g.nextOrderID++
		g.activeOrderIDs = append(g.activeOrderIDs, id)

		// Price in middle band
		price := int64(1000 + g.rng.NextN(2000))
		qty := int64(1 + g.rng.NextN(100))

		return book.Command{
			Kind:    book.CmdNewLimit,
			Side:    side,
			OrderID: id,
			Price:   price,
			Qty:     qty,
		}

	case roll < 80:
		// Cancel
		idx := g.rng.NextN(uint64(len(g.activeOrderIDs)))
		id := g.activeOrderIDs[idx]
		// Remove from active list
		last := len(g.activeOrderIDs) - 1
		g.activeOrderIDs[idx] = g.activeOrderIDs[last]
		g.activeOrderIDs = g.activeOrderIDs[:last]

		return book.Command{
			Kind:    book.CmdCancel,
			Side:    side,
			OrderID: id,
		}

	case roll < 90:
		// ReplaceDown: pick a live order and shrink its quantity.
		idx := g.rng.NextN(uint64(len(g.activeOrderIDs)))
		id := g.activeOrderIDs[idx]
		qty := int64(1 + g.rng.NextN(50))

		return book.Command{
			Kind:    book.CmdReplaceDown,
			Side:    side,
			OrderID: id,
			Qty:     qty,
		}

	default:
		// MarketCross: a new aggressive order that walks the opposite side.
		// Price is the aggressiveness bound: a bid sweeps asks up to the top
		// of the band; an ask sweeps bids down to zero.
		id := g.nextOrderID
		g.nextOrderID++

		price := int64(book.PriceBandSize - 1)
		if side == book.SideAsk {
			price = 0
		}
		qty := int64(1 + g.rng.NextN(50))

		return book.Command{
			Kind:    book.CmdMarketCross,
			Side:    side,
			OrderID: id,
			Price:   price,
			Qty:     qty,
		}
	}
}
