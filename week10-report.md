## Project Plan and Recent Progress

Our ultimate goal is to build a scalable, distributed web-cache platform that can plug in multiple consistent-hash overlays (Chord, Koorde, and a baseline simple hash). The system should run across multiple AWS instances, expose an HTTP API, and collect metrics so we can compare latency, hit rate, replication cost, and request throughput under load.

**Current progress.** We now have both the Chord and Koorde overlays implemented in Go. Nodes can be launched with either overlay, participate in the ring, and serve cache requests. New helper scripts (`scripts/start-nodes.ps1`, `scripts/test-nodes.ps1`) spin up local clusters and smoke-test routing. Automated unit tests cover Koorde pointer maintenance, successor selection, and large-ring lookups (40 nodes) to guard against regressions.

**Next steps.** Finish the simple-hash baseline, package the node into a deployable artifact, and stand up a small AWS deployment (e.g., EC2 or ECS). After that we will run controlled load tests to capture latency distributions, hop counts, hit ratios, and replication overhead for each overlay.

**Team contribution**
Anran Lyu: Implemented code, understood Chord, and taught Yuzheng Shi.
Yuzheng Shi: Implemented code, understood Koorde, and taught Anran Lyu

## Related Work

Our research is grounded in two fundamental papers:

1. M. Frans Kaashoek and David R. Karger.
   Koorde: A simple degree-optimal distributed hash table.
   In _Proceedings of the 2nd International Workshop on Peer-to-Peer Systems (IPTPS ’03)_, February 2003.
2. Ion Stoica, Robert Morris, David Karger, M. Frans Kaashoek, and Hari Balakrishnan.
   Chord: A Scalable Peer-to-Peer Lookup Service for Internet Applications.
   In _Proceedings of the ACM SIGCOMM Conference on Applications, Technologies, Architectures, and Protocols for Computer Communication (SIGCOMM ’01)_, pages 149–160, August 2001.

We also referred to <https://github.com/macvincent/slbdch>. They implemented a non-distributed web cache that compared hash strategies; our work pushes that idea into a fully distributed setting, adds Koorde’s de Bruijn routing, and layers in replication plus observability.

## Impact

Edge caching sits at the core of modern web performance. By delivering a modular overlay layer (Chord vs. Koorde vs. simple hash) atop the same caching pipeline, we can quantify how routing choices affect cache efficiency, latency, and fault tolerance in practice. The resulting data set—especially once we run the AWS load tests—should help other engineers decide when the extra routing complexity of Koorde is justified, how replication settings trade off against hop count, and what operational tooling (health checks, metrics) is necessary to run such overlays in production-like environments.
