// Package customers holds customer records.
package customers

import "fmt"

// Customer is a customer.
type Customer struct {
	ID   CustomerID
	Name string
}

// CustomerID is customer identity.
type CustomerID int

// Display is the display name.
func Display(c Customer) string { return fmt.Sprintf("%s (%d)", c.Name, c.ID) }

func hidden(c Customer) int { return int(c.ID) }
