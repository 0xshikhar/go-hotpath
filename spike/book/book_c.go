package book

// OrderC is completely pointer-free.
type OrderC struct {
	id    uint64
	price int64
	qty   int64
	side  Side
	prev  int32
	next  int32
}

type LevelC struct {
	head  int32
	tail  int32
	count int32
	qty   int64
}

// FlatMapC is an open-addressed hash map with backward-shift deletion.
// Capacity must be a power of two. Zero pointers.
type FlatMapC struct {
	keys   []uint64
	values []int32
	mask   uint32
	count  int
}

func NewFlatMapC(capPow2 int) *FlatMapC {
	return &FlatMapC{
		keys:   make([]uint64, capPow2),
		values: make([]int32, capPow2),
		mask:   uint32(capPow2 - 1),
	}
}

func (m *FlatMapC) Reset() {
	clear(m.keys)
	clear(m.values)
	m.count = 0
}

//hotpath:noalloc
func hash64(x uint64) uint32 {
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x = x ^ (x >> 31)
	return uint32(x)
}

//hotpath:noalloc
func (m *FlatMapC) Put(key uint64, val int32) bool {
	if key == 0 {
		return false
	}
	idx := hash64(key) & m.mask
	for {
		k := m.keys[idx]
		if k == 0 {
			m.keys[idx] = key
			m.values[idx] = val
			m.count++
			return true
		}
		if k == key {
			m.values[idx] = val
			return true
		}
		idx = (idx + 1) & m.mask
	}
}

//hotpath:noalloc
func (m *FlatMapC) Get(key uint64) (int32, bool) {
	if key == 0 {
		return 0, false
	}
	idx := hash64(key) & m.mask
	for {
		k := m.keys[idx]
		if k == 0 {
			return 0, false
		}
		if k == key {
			return m.values[idx], true
		}
		idx = (idx + 1) & m.mask
	}
}

//hotpath:noalloc
func (m *FlatMapC) Delete(key uint64) bool {
	if key == 0 {
		return false
	}
	idx := hash64(key) & m.mask
	for {
		k := m.keys[idx]
		if k == 0 {
			return false
		}
		if k == key {
			break
		}
		idx = (idx + 1) & m.mask
	}

	// Backward shift deletion to avoid tombstones
	m.count--
	curr := idx
	for {
		next := (curr + 1) & m.mask
		k := m.keys[next]
		if k == 0 {
			break
		}
		ideal := hash64(k) & m.mask
		// Check if ideal <= curr (modulo mask)
		diffCurr := (curr - ideal) & m.mask
		diffNext := (next - ideal) & m.mask
		if diffCurr < diffNext {
			m.keys[curr] = k
			m.values[curr] = m.values[next]
			curr = next
		} else {
			break
		}
	}
	m.keys[curr] = 0
	m.values[curr] = 0
	return true
}

type BookC struct {
	orders     []OrderC
	generation []uint32
	freeList   []int32
	orderMap   *FlatMapC
	bids       [PriceBandSize]LevelC
	asks       [PriceBandSize]LevelC
	live       int
}

func NewBookC() *BookC {
	b := &BookC{
		orders:     make([]OrderC, MaxCapacity+1),
		generation: make([]uint32, MaxCapacity+1),
		freeList:   make([]int32, MaxCapacity),
		orderMap:   NewFlatMapC(262144), // Power of 2, > 2x MaxCapacity
	}
	b.Reset()
	return b
}

func (b *BookC) LiveOrders() int {
	return b.live
}

func (b *BookC) Reset() {
	b.live = 0
	b.freeList = b.freeList[:0]
	for i := int32(MaxCapacity); i >= 1; i-- {
		b.freeList = append(b.freeList, i)
	}
	clear(b.orders)
	clear(b.generation)
	b.orderMap.Reset()
	for i := range b.bids {
		b.bids[i] = LevelC{}
		b.asks[i] = LevelC{}
	}
}

//hotpath:noalloc
func (b *BookC) allocSlot() int32 {
	n := len(b.freeList)
	if n == 0 {
		return 0
	}
	slot := b.freeList[n-1]
	b.freeList = b.freeList[:n-1]
	b.live++
	return slot
}

//hotpath:noalloc
func (b *BookC) freeSlot(slot int32) {
	b.generation[slot]++
	b.orders[slot] = OrderC{}
	b.freeList = append(b.freeList, slot) //hotpath:allow free list is preallocated at capacity
	b.live--
}

//hotpath:noalloc
func (b *BookC) Apply(cmd *Command, ev *Event) {
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

		b.orders[slot] = OrderC{
			id:    cmd.OrderID,
			price: cmd.Price,
			qty:   cmd.Qty,
			side:  cmd.Side,
			prev:  0,
			next:  0,
		}
		b.orderMap.Put(cmd.OrderID, slot)

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
		slot, ok := b.orderMap.Get(cmd.OrderID)
		if !ok {
			ev.Kind = EventRejected
			return
		}
		b.orderMap.Delete(cmd.OrderID)
		ord := &b.orders[slot]

		lvl := &b.bids[ord.price]
		if ord.side == SideAsk {
			lvl = &b.asks[ord.price]
		}

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
		slot, ok := b.orderMap.Get(cmd.OrderID)
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
						b.orderMap.Delete(top.id)
						b.freeSlot(slot)
					}
				}
			}
		} else {
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
						b.orderMap.Delete(top.id)
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
