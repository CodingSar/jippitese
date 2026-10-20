package billing

import "time"

// AccountID is account identity.
type AccountID int

// Account is a customer account.
type Account struct {
	ID      AccountID
	Balance Money
}

// OrderID is order identity.
type OrderID int

// Line is one order line.
type Line struct {
	Price Money `json:"price"`
	Qty   int   `json:"qty"`
}

// Gateway is where to charge.
type Gateway struct {
	URL string
}

// Receipt is what the gateway returns.
type Receipt struct {
	ID     string    `json:"id"`
	Amount int       `json:"amount"`
	At     time.Time `json:"at"`
}

// ChargeReq is the wire request.
type ChargeReq struct {
	Order  OrderID `json:"order"`
	Amount Money   `json:"amount"`
}

// BillReq is a service request.
type BillReq struct {
	Order Order `json:"order"`
}

// Tier is a risk tier.
type Tier int

const (
	Low Tier = iota
	Medium
	High
)

// Summary is a reviewer summary.
type Summary struct {
	Headline string
	Risk     Tier
}

// Review is what a human approver is shown.
type Review struct {
	Title   string
	Body    string
	Timeout time.Duration
}
