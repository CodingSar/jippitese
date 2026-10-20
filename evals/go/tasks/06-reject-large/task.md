project: shop
package: orders
---
The indexer in package `orders` must also reject an order whose total is
above 100_000, with `&RejectedError{"too large"}`, so it never counts
towards a customer's total. An order of exactly 100_000 is accepted.
