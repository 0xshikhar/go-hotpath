package book

type OrderB struct {
	id    uint64
	side  Side
	price int64
	qty   int64
	prev  int32
	next  int32
}

type LevelB struct {
	head  int32
	tail  int32
	count int32
	qty   int64
}

type BookB struct {
	orders   []OrderB
	freeList []int32
	orderMap map[uint64]int32
	bids     [PriceBandSize]LevelB
	asks     [PriceBandSize]LevelB
	live     int
}

func NewBookB() *BookB {
	b := &BookB{
		orders:   make([]OrderB, MaxCapacity+1),
		freeList: make([]int32, MaxCapacity),
		orderMap: make(map[uint64]int32, MaxCapacity),
	}
	b.Reset()
	return b
}

func (b *BookB) LiveOrders() int {
	return b.live
}

func (b *BookB) Reset() {
	b.live = 0
	b.freeList = b.freeList[:0]
	for i := int32(MaxCapacity); i >= 1; i-- {
		b.freeList = append(b.freeList, i)
	}
	clear(b.orderMap)
	for i := range b.bids {
		b.bids[i] = LevelB{}
		b.asks[i] = LevelB{}
	}
}

func (b *BookB) allocSlot() int32 {
	n := len(b.freeList)
	if n == 0 {
		return 0 // exhausted
	}
	idx := b.freeList[n-1]
	b.freeList = b.freeList[:n-1]
	b.live++
	return idx
}

func (b *BookB) freeSlot(idx int32) {
	b.freeList = append(b.freeList, idx)
	b.orders[idx] = OrderB{}
	b.live--
}

func (b *BookB) Apply(cmd *Command, ev *Event) {
	ev.Kind = EventNone
	ev.OrderID = cmd.OrderID
	ev.Price = cmd.Price
	ev.ExecutedQty = 0
	ev.RemainingQty = cmd.Qty

	switch cmd.Kind {
	case CmdNewLimit:
		if cmd.Price < 0 || cmd.Price >= PriceBandSize {
			ev.Kind = EventRejected
			return
		}
		slot := b.allocSlot()
		if slot == 0 {
			ev.Kind = EventRejected
			return
		}

		b.orders[slot] = OrderB{
			id:    cmd.OrderID,
			side:  cmd.Side,
			price: cmd.Price,
			qty:   cmd.Qty,
			prev:  0,
			next:  0,
		}
		b.orderMap[cmd.OrderID] = slot

		lvl := &b.bids[cmd.Price]
		if cmd.Side == SideAsk {
			lvl = &b.asks[cmd.Price]
		}

		if lvl.tail == 0 {
			lvl.head = slot
			lvl.tail = slot
		} else {
			b.orders[lvl.tail].next = slot
			b.orders[slot].prev = lvl.tail
			lvl.tail = slot
		}
		lvl.count++
		lvl.qty += cmd.Qty

		ev.Kind = EventAccepted
		ev.RemainingQty = cmd.Qty

	case CmdCancel:
		slot, ok := b.orderMap[cmd.OrderID]
		if !ok {
			ev.Kind = EventRejected
			return
		}
		delete(b.orderMap, cmd.OrderID)
		ord := &b.orders[slot]

		lvl := &b.bids[ord.price]
		if ord.side == SideAsk {
			lvl = &b.asks[ord.price]
		}

		// Detach from level
		if ord.prev != 0 {
			b.orders[ord.prev].next = ord.next
		} else {
			lvl.head = ord.next
		}
		if ord.next != 0 {
			b.orders[ord.next].prev = ord.prev
		} else {
			lvl.tail = ord.prev
		}
		lvl.count--
		lvl.qty -= ord.qty

		b.freeSlot(slot)
		ev.Kind = EventCanceled
		ev.RemainingQty = 0

	case CmdReplaceDown:
		slot, ok := b.orderMap[cmd.OrderID]
		if !ok {
			ev.Kind = EventRejected
			return
		}
		ord := &b.orders[slot]
		if cmd.Qty < ord.qty {
			diff := ord.qty - cmd.Qty
			ord.qty = cmd.Qty

			lvl := &b.bids[ord.price]
			if ord.side == SideAsk {
				lvl = &b.asks[ord.price]
			}
			lvl.qty -= diff

			ev.Kind = EventReplaced
			ev.RemainingQty = ord.qty
		} else {
			ev.Kind = EventRejected
		}

	case CmdMarketCross:
		rem := cmd.Qty
		if cmd.Side == SideBid {
			// Walk asks upward from 0 to cmd.Price
			maxPrice := cmd.Price
			if maxPrice >= PriceBandSize {
				maxPrice = PriceBandSize - 1
			}
			for p := int64(0); p <= maxPrice && rem > 0; p++ {
				lvl := &b.asks[p]
				for lvl.head != 0 && rem > 0 {
					slot := lvl.head
					top := &b.orders[slot]
					tradeQty := top.qty
					if tradeQty > rem {
						tradeQty = rem
					}
					top.qty -= tradeQty
					lvl.qty -= tradeQty
					rem -= tradeQty
					ev.ExecutedQty += tradeQty

					if top.qty == 0 {
						lvl.head = top.next
						if lvl.head != 0 {
							b.orders[lvl.head].prev = 0
						} else {
							lvl.tail = 0
						}
						lvl.count--
						delete(b.orderMap, top.id)
						b.freeSlot(slot)
					}
				}
			}
		} else {
			// Walk bids downward from PriceBandSize-1 down to cmd.Price
			minPrice := cmd.Price
			if minPrice < 0 {
				minPrice = 0
			}
			for p := int64(PriceBandSize - 1); p >= minPrice && rem > 0; p-- {
				lvl := &b.bids[p]
				for lvl.head != 0 && rem > 0 {
					slot := lvl.head
					top := &b.orders[slot]
					tradeQty := top.qty
					if tradeQty > rem {
						tradeQty = rem
					}
					top.qty -= tradeQty
					lvl.qty -= tradeQty
					rem -= tradeQty
					ev.ExecutedQty += tradeQty

					if top.qty == 0 {
						lvl.head = top.next
						if lvl.head != 0 {
							b.orders[lvl.head].prev = 0
						} else {
							lvl.tail = 0
						}
						lvl.count--
						delete(b.orderMap, top.id)
						b.freeSlot(slot)
					}
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
