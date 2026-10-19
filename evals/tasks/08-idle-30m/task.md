project: sessions
module: sessions.store
breaking:
---
The sweeper in `sessions.store` removes sessions idle for more than an
hour. Lower the limit to 30 minutes.
