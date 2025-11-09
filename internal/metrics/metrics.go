package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	cacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cache_hits_total",
		Help: "Total number of cache hits.",
	}, []string{"node"})

	cacheMisses = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cache_misses_total",
		Help: "Total number of cache misses.",
	}, []string{"node"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cache_request_duration_seconds",
		Help:    "Latency for cache requests.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "status"})

	lookupHops = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "overlay_lookup_hops",
		Help:    "Observed hop counts during overlay lookups.",
		Buckets: prometheus.LinearBuckets(1, 1, 10),
	})

	replicationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cache_replication_seconds",
		Help:    "Time spent replicating cache items to successors.",
		Buckets: prometheus.DefBuckets,
	})
)

func init() {
	prometheus.MustRegister(cacheHits, cacheMisses, requestDuration, lookupHops, replicationSeconds)
}

// Handler returns HTTP handler exposing metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// CacheHit increments hit metric.
func CacheHit(node string) {
	cacheHits.WithLabelValues(node).Inc()
}

// CacheMiss increments miss metric.
func CacheMiss(node string) {
	cacheMisses.WithLabelValues(node).Inc()
}

// ObserveRequest records request duration and status.
func ObserveRequest(method, status string, seconds float64) {
	requestDuration.WithLabelValues(method, status).Observe(seconds)
}

// ObserveLookup records overlay hop counts.
func ObserveLookup(hops int) {
	lookupHops.Observe(float64(hops))
}

// ObserveReplication records replication latency.
func ObserveReplication(seconds float64) {
	replicationSeconds.Observe(seconds)
}

