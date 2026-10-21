---
"@zenius-one/telemetry-readthrough-prom-go": patch
---

Require the released `readthrough` v0.1.0 and drop the bootstrap `replace`, so the module builds for consumers (v0.1.0 required an untagged `readthrough v0.0.0`).
