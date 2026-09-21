# cmd

Go entrypoints.

| Command | Purpose |
|---|---|
| `render` | `-write` regenerates the client configs and skill copies; `-check` fails on drift, invalid shape, or a ban violation. See [internal/render](../internal/render/README.md). |

Liveness probes are added under CHAOS-6204.
