# telemetry

Generic, reusable telemetry building blocks for services, as a pnpm + Turborepo + Changesets monorepo of Go modules.

Nothing here knows about a specific product; consumers adapt these packages through small interfaces.

<!--prettier-ignore-->
| Package | Module | What it is |
| --- | --- | --- |
| [`packages/readthrough`](packages/readthrough) | `github.com/zenius-one/telemetry/packages/readthrough` | Read-through cache protection over a [`cachestore`](https://github.com/zenius-one/cachestore) store: stampede, delayed stampede, penetration, avalanche. |
| [`packages/readthrough/prom`](packages/readthrough/prom) | `github.com/zenius-one/telemetry/packages/readthrough/prom` | Prometheus `Observer` for `readthrough`, plus the series the alerts and dashboards are written against. |

```sh
pnpm install
pnpm turbo run build lint check-types test
```

Process for contributors and agents lives in [`.ai/`](.ai/README.md). Licence: MIT.
