# slbwc – Serverless-Like Browser Web Cache

`slbwc` is a Go implementation of a peer-to-peer web cache. Each instance joins a Chord or Koorde distributed hash table, exposes cache operations over HTTP, gossips membership for liveness, replicates cached objects, and exports Prometheus metrics.

## Features

- **Overlay network** – Pluggable Chord or Koorde routing with stabilization, successor maintenance, and de Bruijn pointers.
- **Membership gossip** – SWIM-inspired gossip/cleanup to track healthy peers.
- **Distributed cache** – In-memory TTL-aware LRU with background cleanup and replication.
- **HTTP API** – `GET`/`HEAD /cache?url=...`, optional proxy or redirect behaviour.
- **Observability** – Structured JSON logs and `/metrics` endpoint for Prometheus scrape.

## Prerequisites

- Go >= 1.25
- Network reachability between nodes (for HTTP RPCs).
- Optional: Prometheus for metrics scraping.

On Windows PowerShell, ensure `$env:PATH` contains the Go toolchain (e.g. `C:\Go\bin`).

## Getting Started

Clone the repository, install dependencies, and build:

```powershell
git clone https://github.com/your-org/slbwc.git
cd slbwc
go mod tidy
go build ./...
```

> **Note**  
> `go mod tidy` is optional if you already ran it. It downloads dependencies and writes `go.sum`.

## Running a Single Node

The entrypoint lives in `cmd/node`. Run it directly:

```powershell
go run ./cmd/node `
  --address 127.0.0.1:8080 `
  --cache-capacity 512 `
  --default-ttl 5m
```

> PowerShell treats the backtick (`) as a line-continuation character. Make sure it is the last character on each continued line (no trailing spaces), or run the command on a single line instead.

To experiment with the Koorde overlay instead of Chord:

```powershell
go run ./cmd/node `
  --overlay koorde `
  --address 127.0.0.1:8080
```

Flags (all optional):

| Flag                     | Description                                                                                          | Default          |
| ------------------------ | ---------------------------------------------------------------------------------------------------- | ---------------- |
| `--address`              | Address advertised to the cluster (`host:port`). Also used as bind address unless `--bind` supplied. | `127.0.0.1:8080` |
| `--bind`                 | HTTP listen address (useful when advertising via external IP).                                       | Uses `--address` |
| `--id`                   | Stable seed for hashing. Defaults to `--address`.                                                    |                  |
| `--overlay`              | Overlay algorithm: `chord` or `koorde`.                                                              | `chord`          |
| `--mode`                 | Cache routing mode: `proxy` or `redirect`.                                                           | `proxy`          |
| `--replicas`             | Number of replicas (successors) to store content.                                                    | `3`              |
| `--cache-capacity`       | Entry capacity of local LRU cache.                                                                   | `512`            |
| `--default-ttl`          | TTL applied when origin omits caching headers.                                                       | `5m`             |
| `--cache-janitor-period` | Expired entry cleanup interval.                                                                      | `30s`            |
| `--origin-timeout`       | Timeout for origin fetches and intra-cluster calls.                                                  | `10s`            |
| `--seed`                 | Seed node address. Supply multiple times for redundancy.                                             | (none)           |

The process logs to stdout in JSON. `/healthz` returns `204` when ready.

## Forming a Cluster

1. Start the first node without seeds (it will create a new ring):

   ```powershell
   go run ./cmd/node --address 127.0.0.1:8080
   ```

2. Start additional nodes pointing at the first:

   ```powershell
   go run ./cmd/node `
     --address 127.0.0.1:8081 `
     --seed 127.0.0.1:8080

   go run ./cmd/node `
     --address 127.0.0.1:8082 `
     --seed 127.0.0.1:8080 `
     --seed 127.0.0.1:8081   # optional extra seed
   ```

Nodes join the ring, pull successor lists, and gossip membership. Logs include join success/failure events.

> Ensure every node in the cluster is started with the same `--overlay` selection so they speak the same routing protocol.

## Issuing Requests

- Fetch and cache:
  ```powershell
  Invoke-WebRequest -Uri "http://127.0.0.1:8081/cache?url=https://example.com" -Method GET
  ```
- Validate HEAD behaviour:
  ```powershell
  Invoke-WebRequest -Uri "http://127.0.0.1:8082/cache?url=https://example.com" -Method HEAD
  ```

Any node can handle the request. It will locate the responsible owner via `find_successor`, proxy or redirect the call, and replicate the asset to successors.

## Metrics & Observability

- `GET /metrics` – Prometheus exposition with counters (`cache_hits_total`, `cache_misses_total`), histograms (`cache_request_duration_seconds`, `overlay_lookup_hops`, `cache_replication_seconds`), etc.
- `GET /healthz` – Liveness probe (HTTP 204).
- Logs – JSON fields include method, path, status, and latency as well as overlay/membership warnings.

Sample Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: slbwc
    static_configs:
      - targets: ['127.0.0.1:8080', '127.0.0.1:8081', '127.0.0.1:8082']
```

## Development Tips

- Run tests / build: `go test ./...`, `go build ./...`
- Use `go run ./cmd/node --help` to view flag usage.
- For debugging overlay state, hit the internal endpoints (e.g. `GET /internal/chord/successor` or `GET /internal/koorde/successor`), bearing in mind they are not authenticated.
- To launch a local mini-cluster quickly, use `scripts\start-nodes.ps1` (builds the binary, starts multiple processes, and stops them on Enter) and validate behaviour via `scripts\test-nodes.ps1`.

## Repository Layout

```
cmd/node          Node executable entrypoint
internal/cache    TTL-aware LRU implementation
internal/chord    Chord overlay logic & HTTP RPCs
internal/koorde   Koorde overlay (de Bruijn pointers)
internal/overlay  Shared overlay identifiers & interfaces
internal/membership  Gossip-based membership
internal/metrics  Prometheus collectors
prompt.md         Original requirements overview
```

## License

MIT (adjust if your project uses a different license).
