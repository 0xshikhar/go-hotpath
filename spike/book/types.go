package book

type CommandKind uint8

const (
	CmdNone CommandKind = iota
	CmdNewLimit
	CmdCancel
	CmdReplaceDown
	CmdMarketCross
)

type Side uint8

const (
	SideBid Side = 1
	SideAsk Side = 2
)

type Command struct {
	Kind      CommandKind
	Side      Side
	OrderID   uint64
	Price     int64 // Ticks (0..4095)
	Qty       int64
	Timestamp int64
}

type EventKind uint8

const (
	EventNone EventKind = iota
	EventAccepted
	EventCanceled
	EventReplaced
	EventFilled
	EventPartialFill
	EventRejected
)

type Event struct {
	Kind         EventKind
	OrderID      uint64
	Price        int64
	ExecutedQty  int64
	RemainingQty int64
}

type Book interface {
	Apply(cmd *Command, ev *Event)
	LiveOrders() int
	Reset()
}

const (
	PriceBandSize = 4096
	MaxCapacity   = 100_000
)
