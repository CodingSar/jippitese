project: orders
module: orders.billing
breaking: orders.billing
---
Add a `Refunded` variant to `Status` in `orders.billing`, and a
`pub fn is_final(s: Status) -> Bool` that is true for `Paid`, `Failed`
and `Refunded` and false for `Pending`. Adding a variant to a non-opaque
enum is a breaking change: accept it for `orders.billing`.
