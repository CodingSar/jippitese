project: rates
module: rates.cache
breaking: rates.cache
---
`quote` in `rates.cache` answers 10000 for any unknown currency. Make
unknown currencies an error instead: `quote` returns `Option<Int>` (`None`
for anything but EUR and GBP), and `rate` returns `Result<Int, RateError>`
with a new `pub enum RateError { Cache(CacheError), Unknown(Str) }`, where
`Unknown` carries the currency code. Accept the breaking change for
`rates.cache`.
