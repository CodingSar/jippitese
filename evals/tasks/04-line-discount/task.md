project: orders
module: orders.billing
breaking: orders.billing
---
Give every order line a discount: add a field `discount: Money` to `Line`
in `orders.billing`, and make `total` subtract each line's discount from
that line's `price * qty`. Adding a field to a non-opaque type is a
breaking change: accept it for `orders.billing`.
