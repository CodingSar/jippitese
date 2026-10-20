package billing

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/quick"
)

// positiveMoney draws an amount in 1..1_000_000.
func positiveMoney(n uint32) Money { return Money(n%1_000_000 + 1) }

func TestWithdrawNeverOverdraws(t *testing.T) {
	f := func(acc Account, n uint32) bool {
		a, err := Withdraw(acc, positiveMoney(n))
		return err != nil || a.Balance >= 0
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestWithdrawingTheWholeBalanceEmptiesTheAccount(t *testing.T) {
	f := func(acc Account) bool {
		if acc.Balance <= 0 {
			return true
		}
		a, err := Withdraw(acc, acc.Balance)
		return err == nil && a.Balance == 0
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

type reply struct {
	status int
	body   string
}

// fakeGateway answers each post with the next reply, repeating the last
// one, and counts the posts.
func fakeGateway(t *testing.T, replies ...reply) (Gateway, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp := replies[min(int(n.Add(1))-1, len(replies)-1)]
		w.WriteHeader(rp.status)
		w.Write([]byte(rp.body))
	}))
	t.Cleanup(srv.Close)
	return Gateway{URL: srv.URL + "/charge"}, &n
}

func TestChargeSendsTheOrderTotal(t *testing.T) {
	gw, posts := fakeGateway(t, reply{200, `{"id":"r1","amount":500}`})
	o := Order{ID: 1, Total: 500}
	if _, err := Charge(context.Background(), o, gw); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", posts.Load())
	}
}

func TestWithdrawTable(t *testing.T) {
	for _, tc := range []struct {
		bal, amt Money
		ok       bool
	}{
		{500, 200, true},
		{100, 200, false},
	} {
		_, err := Withdraw(Account{ID: 1, Balance: tc.bal}, tc.amt)
		if (err == nil) != tc.ok {
			t.Errorf("Withdraw(%d, %d): err = %v, want ok = %v", tc.bal, tc.amt, err, tc.ok)
		}
	}
}

func TestReadFileReturnsTheBytesWritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadFile(p)
	if err != nil || !bytes.Equal(b, []byte("hi")) {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
}
