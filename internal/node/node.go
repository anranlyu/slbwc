package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"slbwc/internal/cache"
	"slbwc/internal/chord"
	"slbwc/internal/koorde"
	"slbwc/internal/membership"
	"slbwc/internal/metrics"
	"slbwc/internal/overlay"
)

const (
	internalReplicationPath = "/internal/cache/replicate"
	forwardedHeader         = "X-Slbwc-Forwarded"
)

// Node glues overlay, cache, membership, and HTTP layers together.
type Node struct {
	cfg Config

	self          overlay.RemoteNode
	routing       overlay.Routing
	overlayCancel context.CancelFunc
	membership    *membership.Manager
	cache         *cache.LRU
	httpServer    *http.Server
	logger        *slog.Logger
	mode          Mode
	replicas      int
	originClient  *http.Client

	mu        sync.RWMutex
	started   bool
	startOnce sync.Once
	stopOnce  sync.Once
}

// New creates a Node from configuration.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Node, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	idKey := cfg.ID
	if idKey == "" {
		idKey = cfg.Address
	}
	self := overlay.RemoteNode{
		ID:      overlay.HashKey(idKey),
		Address: cfg.Address,
	}

	var routing overlay.Routing
	switch cfg.Overlay {
	case OverlayKoorde:
		koordeCfg := koorde.DefaultConfig()
		koordeCfg.Logger = logger.With("component", "koorde")
		rpcClient := koorde.NewHTTPClient(3 * time.Second)
		routing = koorde.NewNode(self, rpcClient, koordeCfg)
	default:
		chordCfg := chord.DefaultConfig()
		chordCfg.Logger = logger.With("component", "chord")
		rpcClient := chord.NewHTTPClient(3 * time.Second)
		routing = chord.NewNode(self, rpcClient, chordCfg)
	}

	memCfg := membership.DefaultConfig()
	memCfg.Logger = logger.With("component", "membership")
	memCfg.Seeds = cfg.Seeds
	membershipMgr := membership.NewManager(self, memCfg)

	cacheStore := cache.NewLRU(cfg.CacheCapacity)
	cacheStore.StartJanitor(ctx, cfg.CacheJanitorPeriod)

	originClient := &http.Client{
		Timeout: cfg.OriginTimeout,
	}

	n := &Node{
		cfg:          cfg,
		self:         self,
		routing:      routing,
		membership:   membershipMgr,
		cache:        cacheStore,
		logger:       logger.With("component", "node", "address", cfg.Address, "overlay", string(cfg.Overlay)),
		mode:         cfg.Mode,
		replicas:     cfg.ReplicationFactor,
		originClient: originClient,
	}

	mux := http.NewServeMux()
	routing.RegisterHandlers(mux)
	membership.RegisterHandlers(mux, membershipMgr)
	mux.HandleFunc("/cache", n.handleCache)
	mux.HandleFunc(internalReplicationPath, n.handleReplication)
	mux.HandleFunc("/healthz", n.handleHealth)
	mux.Handle("/metrics", metrics.Handler())

	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = cfg.Address
	}
	n.httpServer = &http.Server{
		Addr:    bindAddr,
		Handler: requestLoggingMiddleware(logger, mux),
	}

	return n, nil
}

// Start boots the node and begins background routines.
func (n *Node) Start(ctx context.Context) error {
	var err error
	n.startOnce.Do(func() {
		n.membership.Start(ctx)
		n.overlayCancel = n.routing.RunBackground(ctx)
		err = n.bootstrap(ctx)
		if err != nil {
			return
		}
		go func() {
			if serveErr := n.httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				n.logger.Error("http server error", slog.String("err", serveErr.Error()))
			}
		}()
		n.mu.Lock()
		n.started = true
		n.mu.Unlock()
	})
	return err
}

// Stop gracefully stops background tasks and the HTTP server.
func (n *Node) Stop(ctx context.Context) error {
	var err error
	n.stopOnce.Do(func() {
		if n.overlayCancel != nil {
			n.overlayCancel()
		}
		err = n.httpServer.Shutdown(ctx)
	})
	return err
}

func (n *Node) bootstrap(ctx context.Context) error {
	if len(n.cfg.Seeds) == 0 {
		n.logger.Info("starting new ring")
		return n.routing.Join(ctx, overlay.RemoteNode{})
	}
	for _, seedAddr := range n.cfg.Seeds {
		if seedAddr == "" || seedAddr == n.self.Address {
			continue
		}
		seed := overlay.RemoteNode{
			ID:      overlay.HashKey(seedAddr),
			Address: seedAddr,
		}
		ctxJoin, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := n.routing.Join(ctxJoin, seed)
		cancel()
		if err != nil {
			n.logger.Warn("failed to join seed", slog.String("seed", seedAddr), slog.String("err", err.Error()))
			continue
		}
		n.membership.MarkAlive(seed)
		n.logger.Info("joined ring via seed", slog.String("seed", seedAddr))
		return nil
	}
	n.logger.Info("no reachable seeds, creating new ring")
	return n.routing.Join(ctx, overlay.RemoteNode{})
}

func (n *Node) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (n *Node) handleCache(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	qURL := r.URL.Query().Get("url")
	if qURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	if !isValidURL(qURL) {
		http.Error(w, "invalid url parameter", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	key := strings.TrimSpace(qURL)
	id := overlay.HashKey(key)
	owner, hops, err := n.routing.FindSuccessor(ctx, id)
	if err != nil {
		http.Error(w, fmt.Sprintf("lookup failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	metrics.ObserveLookup(hops)

	if owner.Address == n.self.Address || r.Header.Get(forwardedHeader) == "1" {
		n.serveLocal(w, r, key, start)
		return
	}

	switch n.mode {
	case RedirectMode:
		target := fmt.Sprintf("http://%s/cache?url=%s", owner.Address, url.QueryEscape(key))
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	default:
		n.proxyToOwner(w, r, owner, key, start)
	}
}

func (n *Node) serveLocal(w http.ResponseWriter, r *http.Request, key string, start time.Time) {
	method := r.Method
	if method != http.MethodGet && method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entry, err := n.cache.Get(key)
	if err == nil {
		metrics.CacheHit(n.self.Address)
		w.Header().Set("Content-Type", entry.ContentType)
		w.Header().Set("X-Cache-Status", "HIT")
		w.Header().Set("X-Cache-TTL", remainingTTL(entry))
		if method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(entry.Body)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		metrics.ObserveRequest(method, "hit", time.Since(start).Seconds())
		return
	}

	if method == http.MethodHead {
		metrics.CacheMiss(n.self.Address)
		status, headers := n.fetchHead(r.Context(), key)
		for k, values := range headers {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-Cache-Status", "MISS")
		w.WriteHeader(status)
		metrics.ObserveRequest(method, fmt.Sprintf("%d", status), time.Since(start).Seconds())
		return
	}

	body, contentType, ttl, status, fetchErr := n.fetchAndStore(r.Context(), key)
	if fetchErr != nil {
		http.Error(w, fetchErr.Error(), status)
		metrics.ObserveRequest(method, fmt.Sprintf("%d", status), time.Since(start).Seconds())
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Cache-Status", "MISS")
	w.Header().Set("X-Cache-TTL", ttl.String())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	metrics.CacheMiss(n.self.Address)
	metrics.ObserveRequest(method, "miss", time.Since(start).Seconds())
}

func (n *Node) fetchHead(ctx context.Context, key string) (int, http.Header) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, key, nil)
	if err != nil {
		return http.StatusBadRequest, http.Header{}
	}
	resp, err := n.originClient.Do(req)
	if err != nil {
		return http.StatusBadGateway, http.Header{}
	}
	defer resp.Body.Close()
	headers := cloneHeaders(resp.Header)
	return resp.StatusCode, headers
}

func (n *Node) fetchAndStore(ctx context.Context, key string) ([]byte, string, time.Duration, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, "", 0, http.StatusBadRequest, err
	}
	resp, err := n.originClient.Do(req)
	if err != nil {
		return nil, "", 0, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", 0, resp.StatusCode, fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 0, http.StatusBadGateway, err
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ttl := parseTTL(resp.Header, n.cfg.DefaultTTL)

	entry := cache.Entry{
		Key:         key,
		Body:        body,
		ContentType: contentType,
		TTL:         ttl,
		StoredAt:    time.Now(),
	}
	n.cache.Set(entry)
	n.replicateAsync(entry)
	return body, contentType, ttl, http.StatusOK, nil
}

func (n *Node) replicateAsync(entry cache.Entry) {
	if n.replicas <= 1 {
		return
	}
	targets := n.routing.SuccessorList()
	if len(targets) == 0 {
		return
	}
	targets = filterSelf(targets, n.self.Address)
	if len(targets) == 0 {
		return
	}
	if len(targets) > n.replicas-1 {
		targets = targets[:n.replicas-1]
	}
	go n.replicate(entry, targets)
}

func (n *Node) replicate(entry cache.Entry, targets []overlay.RemoteNode) {
	payload := struct {
		Key         string    `json:"key"`
		ContentType string    `json:"content_type"`
		Body        []byte    `json:"body"`
		TTLMillis   int64     `json:"ttl_ms"`
		StoredAt    time.Time `json:"stored_at"`
	}{
		Key:         entry.Key,
		ContentType: entry.ContentType,
		Body:        entry.Body,
		TTLMillis:   int64(entry.TTL / time.Millisecond),
		StoredAt:    entry.StoredAt,
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		n.logger.Error("encode replication payload", slog.String("err", err.Error()))
		return
	}
	start := time.Now()
	for _, target := range targets {
		url := fmt.Sprintf("http://%s%s", target.Address, internalReplicationPath)
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf.Bytes()))
		if err != nil {
			n.logger.Warn("replication request build failed", slog.String("target", target.Address), slog.String("err", err.Error()))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := n.originClient.Do(req)
		if err != nil {
			n.logger.Warn("replication request failed", slog.String("target", target.Address), slog.String("err", err.Error()))
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			n.logger.Warn("replication rejected", slog.String("target", target.Address), slog.Int("status", resp.StatusCode))
		}
	}
	metrics.ObserveReplication(time.Since(start).Seconds())
}

func (n *Node) handleReplication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Key         string    `json:"key"`
		ContentType string    `json:"content_type"`
		Body        []byte    `json:"body"`
		TTLMillis   int64     `json:"ttl_ms"`
		StoredAt    time.Time `json:"stored_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if payload.Key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	ttl := time.Duration(payload.TTLMillis) * time.Millisecond
	if ttl <= 0 {
		ttl = n.cfg.DefaultTTL
	}
	entry := cache.Entry{
		Key:         payload.Key,
		Body:        payload.Body,
		ContentType: payload.ContentType,
		TTL:         ttl,
		StoredAt:    payload.StoredAt,
	}
	n.cache.Set(entry)
	w.WriteHeader(http.StatusNoContent)
}

func (n *Node) proxyToOwner(w http.ResponseWriter, r *http.Request, owner overlay.RemoteNode, key string, start time.Time) {
	reqURL := fmt.Sprintf("http://%s/cache?url=%s", owner.Address, url.QueryEscape(key))
	req, err := http.NewRequestWithContext(r.Context(), r.Method, reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set(forwardedHeader, "1")
	resp, err := n.originClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
	metrics.ObserveRequest(r.Method, fmt.Sprintf("%d", resp.StatusCode), time.Since(start).Seconds())
}

func remainingTTL(entry cache.Entry) string {
	if entry.TTL <= 0 {
		return "inf"
	}
	elapsed := time.Since(entry.StoredAt)
	remaining := entry.TTL - elapsed
	if remaining <= 0 {
		return "0s"
	}
	return remaining.Truncate(time.Millisecond).String()
}

func cloneHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for k, values := range src {
		for _, v := range values {
			dst.Add(k, v)
		}
	}
	return dst
}

func copyHeaders(dst, src http.Header) {
	for k, values := range src {
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func parseTTL(header http.Header, defaultTTL time.Duration) time.Duration {
	if cc := header.Get("Cache-Control"); cc != "" {
		directives := strings.Split(cc, ",")
		for _, directive := range directives {
			d := strings.TrimSpace(directive)
			if strings.HasPrefix(d, "max-age=") {
				seconds := strings.TrimPrefix(d, "max-age=")
				val, err := time.ParseDuration(seconds + "s")
				if err == nil {
					return clampDuration(val, 10*time.Second, 24*time.Hour)
				}
			}
		}
	}
	if expires := header.Get("Expires"); expires != "" {
		if ts, err := http.ParseTime(expires); err == nil {
			if ttl := time.Until(ts); ttl > 0 {
				return clampDuration(ttl, 10*time.Second, 24*time.Hour)
			}
		}
	}
	return defaultTTL
}

func clampDuration(val, min, max time.Duration) time.Duration {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func filterSelf(nodes []overlay.RemoteNode, selfAddr string) []overlay.RemoteNode {
	out := make([]overlay.RemoteNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Address == "" || n.Address == selfAddr {
			continue
		}
		out = append(out, n)
	}
	return out
}

func isValidURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

func requestLoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		logger.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// CurrentMembership returns alive members.
func (n *Node) CurrentMembership() []overlay.RemoteNode {
	return n.membership.AliveMembers()
}

// Address returns node address.
func (n *Node) Address() string {
	return n.self.Address
}

// Overlay returns the routing implementation.
func (n *Node) Overlay() overlay.Routing {
	return n.routing
}

// Cache returns cache reference.
func (n *Node) Cache() *cache.LRU {
	return n.cache
}

// OriginTimeout returns origin fetch timeout.
func (n *Node) OriginTimeout() time.Duration {
	return n.cfg.OriginTimeout
}

// DefaultTTL returns TTL used for origin misses.
func (n *Node) DefaultTTL() time.Duration {
	return n.cfg.DefaultTTL
}
