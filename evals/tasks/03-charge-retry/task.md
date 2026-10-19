project: orders
module: orders.billing
breaking:
---
`charge` in `orders.billing` counts attempts while the gateway answers 503
but never posts again. Make it retry: post the same body again while the
status is 503, at most three posts in total, and decode the last response.
