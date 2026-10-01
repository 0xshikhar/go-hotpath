package book

type OrderA struct {
	id    uint64
	side  Side
	price int64
	qty   int64
}

type LevelA struct {
	price  int64
	orders []*OrderA
}

type BookA struct {
	orders map[uint64]*OrderA
	bids   map[int64]*LevelA
	asks   map[int64]*LevelA
}

func NewBookA() *BookA {
	return &BookA{
		orders: make(map[uint64]*OrderA),
		bids:   make(map[int64]*LevelA),
		asks:   make(map[int64]*LevelA),
	}
}

func (b *BookA) LiveOrders() int {
	return len(b.orders)
}

func (b *BookA) Reset() {
	b.orders = make(map[uint64]*OrderA)
	b.bids = make(map[int64]*LevelA)
	b.asks = make(map[int64]*LevelA)
}

//hotpath:noalloc
func (b *BookA) Apply(cmd *Command, ev *Event) {
	ev.Kind = EventNone
	ev.OrderID = cmd.OrderID
	ev.Price = cmd.Price
	ev.ExecutedQty = 0
	ev.RemainingQty = cmd.Qty

	switch cmd.Kind {
	case CmdNewLimit:
		ord := &OrderA{
			id:    cmd.OrderID,
			side:  cmd.Side,
			price: cmd.Price,
			qty:   cmd.Qty,
		}
		b.orders[cmd.OrderID] = ord

		levels := b.bids
		if cmd.Side == SideAsk {
			levels = b.asks
		}

		lvl := levels[cmd.Price]
		if lvl == nil {
			lvl = &LevelA{price: cmd.Price}
			levels[cmd.Price] = lvl
		}
		lvl.orders = append(lvl.orders, ord)

		ev.Kind = EventAccepted
		ev.RemainingQty = cmd.Qty

	case CmdCancel:
		ord, ok := b.orders[cmd.OrderID]
		if !ok {
			ev.Kind = EventRejected
			return
		}
		delete(b.orders, cmd.OrderID)

		levels := b.bids
		if ord.side == SideAsk {
			levels = b.asks
		}
		if lvl, ok := levels[ord.price]; ok {
			for i, o := range lvl.orders {
				if o.id == ord.id {
					lvl.orders = append(lvl.orders[:i], lvl.orders[i+1:]...)
					break
				}
			}
			if len(lvl.orders) == 0 {
				delete(levels, ord.price)
			}
		}

		ev.Kind = EventCanceled
		ev.RemainingQty = 0

	case CmdReplaceDown:
		ord, ok := b.orders[cmd.OrderID]
		if !ok {
			ev.Kind = EventRejected
			return
		}
		if cmd.Qty < ord.qty {
			ord.qty = cmd.Qty
			ev.Kind = EventReplaced
			ev.RemainingQty = ord.qty
		} else {
			ev.Kind = EventRejected
		}

	case CmdMarketCross:
		// Cross opposite side
		rem := cmd.Qty
		var oppLevels map[int64]*LevelA
		if cmd.Side == SideBid {
			oppLevels = b.asks
		} else {
			oppLevels = b.bids
		}

		if cmd.Side == SideBid {
			maxPrice := cmd.Price
			if maxPrice >= PriceBandSize {
				maxPrice = PriceBandSize - 1
			}
			for p := int64(0); p <= maxPrice && rem > 0; p++ {
				lvl, ok := oppLevels[p]
				if !ok {
					continue
				}
				for len(lvl.orders) > 0 && rem > 0 {
					top := lvl.orders[0]
					tradeQty := top.qty
					if tradeQty > rem {
						tradeQty = rem
					}
					top.qty -= tradeQty
					rem -= tradeQty
					ev.ExecutedQty += tradeQty

					if top.qty == 0 {
						lvl.orders = lvl.orders[1:]
						delete(b.orders, top.id)
					}
				}
				if len(lvl.orders) == 0 {
					delete(oppLevels, p)
				}
			}
		} else {
			minPrice := cmd.Price
			if minPrice < 0 {
				minPrice = 0
			}
			for p := int64(PriceBandSize - 1); p >= minPrice && rem > 0; p-- {
				lvl, ok := oppLevels[p]
				if !ok {
					continue
				}
				for len(lvl.orders) > 0 && rem > 0 {
					top := lvl.orders[0]
					tradeQty := top.qty
					if tradeQty > rem {
						tradeQty = rem
					}
					top.qty -= tradeQty
					rem -= tradeQty
					ev.ExecutedQty += tradeQty

					if top.qty == 0 {
						lvl.orders = lvl.orders[1:]
						delete(b.orders, top.id)
					}
				}
				if len(lvl.orders) == 0 {
					delete(oppLevels, p)
				}
			}
		}

		if ev.ExecutedQty > 0 {
			if rem == 0 {
				ev.Kind = EventFilled
			} else {
				ev.Kind = EventPartialFill
			}
			ev.RemainingQty = rem
		} else {
			ev.Kind = EventRejected
		}
	}
}
