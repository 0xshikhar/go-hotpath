package main_test

import (
	"testing"

	"spike/book"
	"spike/driver"
)

func TestCorrectnessAcrossBooks(t *testing.T) {
	bookA := book.NewBookA()
	bookB := book.NewBookB()
	bookC := book.NewBookC()

	gen := driver.NewWorkloadGenerator(0xCAFEBABE)
	var evA, evB, evC book.Event

	const testOps = 20_000
	for i := 0; i < testOps; i++ {
		cmd := gen.NextCommand()

		cmdA := cmd
		cmdB := cmd
		cmdC := cmd

		bookA.Apply(&cmdA, &evA)
		bookB.Apply(&cmdB, &evB)
		bookC.Apply(&cmdC, &evC)

		if evA.Kind != evB.Kind || evA.Kind != evC.Kind {
			t.Fatalf("Step %d (Kind %d, Side %d, Price %d): Event Kind mismatch: A=%d, B=%d, C=%d",
				i, cmd.Kind, cmd.Side, cmd.Price, evA.Kind, evB.Kind, evC.Kind)
		}
		if evA.ExecutedQty != evB.ExecutedQty || evA.ExecutedQty != evC.ExecutedQty {
			t.Fatalf("Step %d: ExecutedQty mismatch: A=%d, B=%d, C=%d",
				i, evA.ExecutedQty, evB.ExecutedQty, evC.ExecutedQty)
		}
		if evA.RemainingQty != evB.RemainingQty || evA.RemainingQty != evC.RemainingQty {
			t.Fatalf("Step %d: RemainingQty mismatch: A=%d, B=%d, C=%d",
				i, evA.RemainingQty, evB.RemainingQty, evC.RemainingQty)
		}

		if bookA.LiveOrders() != bookB.LiveOrders() || bookA.LiveOrders() != bookC.LiveOrders() {
			t.Fatalf("Step %d: LiveOrders mismatch: A=%d, B=%d, C=%d",
				i, bookA.LiveOrders(), bookB.LiveOrders(), bookC.LiveOrders())
		}
	}
	t.Logf("Success! 20,000 commands matched perfectly across Book A, B, and C (Live: %d)", bookA.LiveOrders())
}

func TestAllocsPerRun(t *testing.T) {
	bookC := book.NewBookC()
	gen := driver.NewWorkloadGenerator(0x9999)
	var ev book.Event

	// Prime book
	for i := 0; i < 1000; i++ {
		cmd := gen.NextCommand()
		bookC.Apply(&cmd, &ev)
	}

	cmdLimit := book.Command{Kind: book.CmdNewLimit, Side: book.SideBid, OrderID: 999999, Price: 1500, Qty: 10}
	cmdCancel := book.Command{Kind: book.CmdCancel, OrderID: 999999}

	allocsC := testing.AllocsPerRun(5000, func() {
		bookC.Apply(&cmdLimit, &ev)
		bookC.Apply(&cmdCancel, &ev)
	})

	if allocsC != 0 {
		t.Errorf("Expected BookC to have 0 allocs/run, got: %f", allocsC)
	} else {
		t.Logf("PASS: BookC has exactly 0.00 allocs/run on Apply")
	}

	bookB := book.NewBookB()
	allocsB := testing.AllocsPerRun(5000, func() {
		bookB.Apply(&cmdLimit, &ev)
		bookB.Apply(&cmdCancel, &ev)
	})
	t.Logf("BookB allocs/run: %f", allocsB)

	bookA := book.NewBookA()
	allocsA := testing.AllocsPerRun(5000, func() {
		bookA.Apply(&cmdLimit, &ev)
		bookA.Apply(&cmdCancel, &ev)
	})
	t.Logf("BookA allocs/run: %f (allocating dynamically)", allocsA)
	if allocsA == 0 {
		t.Errorf("Expected BookA to allocate, but got 0")
	}
}
