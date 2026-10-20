package orders

import "testing"

func TestPlacedOrdersAreTotalledPerCustomer(t *testing.T) {
	ix := NewIndexer()
	ix.Deliver(Placed{ID: 1, Customer: 7, Total: 30})
	ix.Deliver(Placed{ID: 2, Customer: 7, Total: 12})
	ix.Deliver(Placed{ID: 3, Customer: 8, Total: 5})
	if got, ok := ix.Totals.Get(7); !ok || got != 42 {
		t.Errorf("total of 7 = %d, %v; want 42", got, ok)
	}
	if got, ok := ix.Totals.Get(8); !ok || got != 5 {
		t.Errorf("total of 8 = %d, %v; want 5", got, ok)
	}
}

func TestARejectedOrderIsDeadLetteredAndCountsNothing(t *testing.T) {
	ix := NewIndexer()
	ix.Deliver(Placed{ID: 4, Customer: 9, Total: -1})
	if got, ok := ix.Totals.Get(9); ok {
		t.Errorf("total of 9 = %d, want none", got)
	}
	if len(ix.Dead) != 1 {
		t.Errorf("dead letters = %v, want the order", ix.Dead)
	}
}

func TestARedeliveredOrderIsCountedOnce(t *testing.T) {
	ix := NewIndexer()
	ix.Deliver(Placed{ID: 5, Customer: 10, Total: 8})
	ix.Deliver(Placed{ID: 5, Customer: 10, Total: 8})
	if got, ok := ix.Totals.Get(10); !ok || got != 8 {
		t.Errorf("total of 10 = %d, %v; want 8", got, ok)
	}
}
