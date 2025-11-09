package membership

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"log/slog"

	"slbwc/internal/chord"
)

// State defines membership state.
type State string

const (
	// StateAlive indicates node is healthy.
	StateAlive State = "alive"
	// StateSuspect indicates we suspect node failure.
	StateSuspect State = "suspect"
	// StateDead indicates node confirmed failed.
	StateDead State = "dead"
)

// Member holds membership metadata.
type Member struct {
	ID          chord.Identifier `json:"id"`
	Address     string           `json:"address"`
	State       State            `json:"state"`
	Incarnation uint64           `json:"incarnation"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// Config for Manager.
type Config struct {
	GossipInterval    time.Duration
	SuspectTimeout    time.Duration
	CleanupInterval   time.Duration
	Seeds             []string
	Logger            *slog.Logger
	HTTPTimeout       time.Duration
	MaxGossipFanout   int
	SuspectProbeCount int
}

// DefaultConfig returns default settings.
func DefaultConfig() Config {
	return Config{
		GossipInterval:    1 * time.Second,
		SuspectTimeout:    5 * time.Second,
		CleanupInterval:   10 * time.Second,
		HTTPTimeout:       2 * time.Second,
		Seeds:             nil,
		Logger:            slog.Default(),
		MaxGossipFanout:   3,
		SuspectProbeCount: 2,
	}
}

// Manager implements a simple gossip-based membership protocol.
type Manager struct {
	self  chord.RemoteNode
	cfg   Config
	rnd   *rand.Rand
	http  *http.Client
	mu    sync.RWMutex
	state map[string]*Member
}

// NewManager constructs membership manager.
func NewManager(self chord.RemoteNode, cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 8,
		MaxIdleConns:        64,
		IdleConnTimeout:     30 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &Manager{
		self:  self,
		cfg:   cfg,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
		http: &http.Client{
			Timeout:   cfg.HTTPTimeout,
			Transport: transport,
		},
		state: make(map[string]*Member),
	}
}

// Start registers the node and begins gossip loops.
func (m *Manager) Start(ctx context.Context) {
	m.registerSelf()
	go m.gossipLoop(ctx)
	go m.cleanupLoop(ctx)
}

func (m *Manager) registerSelf() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[m.self.Address] = &Member{
		ID:        m.self.ID,
		Address:   m.self.Address,
		State:     StateAlive,
		UpdatedAt: time.Now(),
	}
}

func (m *Manager) gossipLoop(ctx context.Context) {
	t := time.NewTicker(m.cfg.GossipInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.spread(ctx)
		}
	}
}

func (m *Manager) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(m.cfg.CleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.reapSuspects()
		}
	}
}

func (m *Manager) reapSuspects() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for addr, member := range m.state {
		if member.Address == m.self.Address {
			continue
		}
		switch member.State {
		case StateAlive:
			if now.Sub(member.UpdatedAt) > 2*m.cfg.SuspectTimeout {
				member.State = StateSuspect
			}
		case StateSuspect:
			if now.Sub(member.UpdatedAt) > m.cfg.SuspectTimeout {
				member.State = StateDead
				member.UpdatedAt = now
			}
		case StateDead:
			if now.Sub(member.UpdatedAt) > 2*m.cfg.SuspectTimeout {
				delete(m.state, addr)
			}
		}
	}
}

func (m *Manager) spread(ctx context.Context) {
	payload := m.snapshot()
	targets := m.sampleTargets()
	for _, target := range targets {
		m.send(ctx, target, payload)
	}
}

func (m *Manager) send(ctx context.Context, target string, payload []byte) {
	if target == m.self.Address {
		return
	}
	url := "http://" + target + "/internal/membership/gossip"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		m.markSuspect(target)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		m.markSuspect(target)
	}
}

func (m *Manager) snapshot() []byte {
	members := m.Members()
	payload, _ := json.Marshal(struct {
		Members []*Member `json:"members"`
	}{Members: members})
	return payload
}

func (m *Manager) sampleTargets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targets := make([]string, 0, len(m.state))
	for addr, member := range m.state {
		if addr == m.self.Address {
			continue
		}
		if member.State == StateDead {
			continue
		}
		targets = append(targets, addr)
	}
	if len(targets) == 0 {
		targets = append(targets, m.cfg.Seeds...)
	}
	m.rnd.Shuffle(len(targets), func(i, j int) {
		targets[i], targets[j] = targets[j], targets[i]
	})
	if m.cfg.MaxGossipFanout > 0 && len(targets) > m.cfg.MaxGossipFanout {
		return targets[:m.cfg.MaxGossipFanout]
	}
	return targets
}

// Merge applies incoming membership gossip.
func (m *Manager) Merge(members []*Member) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, incoming := range members {
		if incoming.Address == "" {
			continue
		}
		if incoming.Address == m.self.Address {
			continue
		}
		cur, ok := m.state[incoming.Address]
		if !ok || incoming.UpdatedAt.After(cur.UpdatedAt) || incoming.Incarnation > cur.Incarnation {
			cloned := *incoming
			if cloned.UpdatedAt.IsZero() {
				cloned.UpdatedAt = now
			}
			m.state[incoming.Address] = &cloned
		}
	}
}

// Members returns a copy of membership list.
func (m *Manager) Members() []*Member {
	m.mu.RLock()
	defer m.mu.RUnlock()
	members := make([]*Member, 0, len(m.state))
	for _, member := range m.state {
		cloned := *member
		members = append(members, &cloned)
	}
	return members
}

// AliveMembers returns addresses of nodes considered alive.
func (m *Manager) AliveMembers() []chord.RemoteNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]chord.RemoteNode, 0, len(m.state))
	for _, member := range m.state {
		if member.State == StateAlive {
			out = append(out, chord.RemoteNode{ID: member.ID, Address: member.Address})
		}
	}
	return out
}

// markSuspect updates suspicion status.
func (m *Manager) markSuspect(address string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if member, ok := m.state[address]; ok {
		if member.State == StateAlive {
			member.State = StateSuspect
			member.UpdatedAt = time.Now()
		}
	} else {
		m.state[address] = &Member{
			Address:   address,
			State:     StateSuspect,
			UpdatedAt: time.Now(),
		}
	}
}

// HandleGossip handles HTTP gossip request.
func (m *Manager) HandleGossip(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Members []*Member `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.Merge(payload.Members)
	w.WriteHeader(http.StatusNoContent)
}

// HandlePing responds to membership ping.
func (m *Manager) HandlePing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// MarkAlive marks a member as alive (used on join acknowledgement).
func (m *Manager) MarkAlive(node chord.RemoteNode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.state[node.Address] = &Member{
		ID:        node.ID,
		Address:   node.Address,
		State:     StateAlive,
		UpdatedAt: now,
	}
}

