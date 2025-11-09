### 🧱 Core Requirements

#### 1. Overlay Network

- Each cache node participates in a **Distributed Hash Table (DHT)** overlay:
  - **Chord** (finger tables, successors)
  - **Koorde** (de Bruijn-style pointers)
- The overlay should support:
  - Node **join** and **leave** operations dynamically.
  - Routing: `findOwner(key)` → return responsible node ID.
  - Recovery and stabilization loops (fix fingers, update successors).

#### 2. Membership & Discovery

- Use a **gossip-based or SWIM-like membership protocol** for liveness and neighbor updates.
- Each node maintains minimal overlay state (successors, predecessors, and overlay-specific pointers).
- Nodes discover each other through one or more **seed nodes** on startup.

#### 3. Cache Functionality

- Each node maintains a **local web cache**:
  - In-memory (use TinyLFU or LRU eviction).
  - Supports TTL, lazy expiration, and background cleanup.
- On a cache **miss**, the node fetches content from origin (HTTP client), stores it locally, and optionally **replicates** to successors.
- Requests should be handled via a simple HTTP API:
  - `GET /cache?url=...`
  - `HEAD /cache?url=...`

#### 4. Request Flow (No Master)

1. Client sends a request to any random node.
2. That node hashes the URL → finds the **responsible node** using either Chord or Koorde routing.
3. It then:
   - **Proxies** the request to the owner node, or
   - **Redirects** the client to the owner (configurable mode).
4. The owner node returns the cached content or fetches and caches it if missing.

#### 5. Replication & Fault Tolerance

- Use **replication factor r (e.g., 3)** to copy cached content to next r–1 successors.
- Reads may serve from any fresh replica.
- When a node fails:
  - Successor automatically takes ownership.
  - Replication repair ensures lost copies are re-created.

#### 6. Observability

- Expose Prometheus-style metrics:
  - Cache hit ratio, lookup latency, hop count, request rate, etc.
- Structured logs with request key and overlay hops.
- Optional OpenTelemetry tracing for multi-hop request paths.

---

### 🧩 Implementation Details

#### Language

- **Go (preferred)** — leverage goroutines, channels, and sync primitives.
