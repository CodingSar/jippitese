project: shop
module: shop.orders
breaking:
---
Add `pub fn grand_total() -> Result<Int, RepoError> with {mut}` to
`shop.orders`: the sum of every customer's running total in `Totals`.
