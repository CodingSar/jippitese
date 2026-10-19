project: shop
module: shop.orders
breaking:
---
The indexer in `shop.orders` must also reject an order whose total is
above 100_000, with `IndexError.Rejected("too large")`, so it never counts
towards a customer's total. An order of exactly 100_000 is accepted.
