package koorde

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"slbwc/internal/overlay"
)

// RPCClient defines the operations required to communicate with remote Koorde nodes.
type RPCClient interface {
	FindSuccessor(ctx context.Context, address string, id overlay.Identifier) (overlay.RemoteNode, int, error)
	GetSuccessor(ctx context.Context, address string) (overlay.RemoteNode, error)
	GetPredecessor(ctx context.Context, address string) (overlay.RemoteNode, error)
	Notify(ctx context.Context, address string, candidate overlay.RemoteNode) error
	Ping(ctx context.Context, address string) error
	GetSuccessorList(ctx context.Context, address string) ([]overlay.RemoteNode, error)
}

// Config contains Koorde maintenance configuration.
type Config struct {
	StabilizeInterval  time.Duration
	FixPointerInterval time.Duration
	CheckPredecessor   time.Duration
	SuccessorListSize  int
	Logger             *slog.Logger
}

// DefaultConfig returns Config with defaults similar to Chord.
func DefaultConfig() Config {
	return Config{
		StabilizeInterval:  2 * time.Second,
		FixPointerInterval: 1500 * time.Millisecond,
		CheckPredecessor:   4 * time.Second,
		SuccessorListSize:  4,
		Logger:             slog.Default(),
	}
}

// Node implements Koorde overlay logic for a single node.
type Node struct {
	mu sync.RWMutex

	self        overlay.RemoteNode
	successor   overlay.RemoteNode
	predecessor overlay.RemoteNode
	debruijn    overlay.RemoteNode
	successors  []overlay.RemoteNode

	cfg Config
	rpc RPCClient
}

// NewNode constructs a Koorde node.
func NewNode(self overlay.RemoteNode, rpc RPCClient, cfg Config) *Node {
	if cfg.SuccessorListSize <= 0 {
		cfg.SuccessorListSize = 4
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	successorList := make([]overlay.RemoteNode, cfg.SuccessorListSize)
	for i := range successorList {
		successorList[i] = self
	}
	return &Node{
		self:        self,
		successor:   self,
		predecessor: overlay.RemoteNode{},
		debruijn:    self,
		successors:  successorList,
		cfg:         cfg,
		rpc:         rpc,
	}
}

// Self returns node identity.
func (n *Node) Self() overlay.RemoteNode {
	return n.self
}

// Successor returns current successor.
func (n *Node) Successor() overlay.RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.successor
}

// SuccessorList returns successor list.
func (n *Node) SuccessorList() []overlay.RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]overlay.RemoteNode, 0, len(n.successors))
	for _, succ := range n.successors {
		if !succ.IsZero() {
			out = append(out, succ)
		}
	}
	return out
}

// Predecessor returns predecessor.
func (n *Node) Predecessor() overlay.RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.predecessor
}

// Join initialises the node by contacting an existing node.
func (n *Node) Join(ctx context.Context, known overlay.RemoteNode) error {
	if known.IsZero() || known.Address == n.self.Address {
		n.mu.Lock()
		n.successor = n.self
		for i := range n.successors {
			n.successors[i] = n.self
		}
		n.debruijn = n.self
		n.mu.Unlock()
		return nil
	}
	succ, _, err := n.rpc.FindSuccessor(ctx, known.Address, n.self.ID)
	if err != nil {
		return fmt.Errorf("join: find successor: %w", err)
	}
	n.mu.Lock()
	n.successor = succ
	n.successors[0] = succ
	n.mu.Unlock()
	return nil
}

// FindSuccessor resolves the node responsible for id.
func (n *Node) FindSuccessor(ctx context.Context, id overlay.Identifier) (overlay.RemoteNode, int, error) {
	n.mu.RLock()
	succ := n.successor
	self := n.self
	n.mu.RUnlock()

	if id.BetweenLeftInclusive(self.ID, succ.ID) {
		return succ, 1, nil
	}

	candidate := n.closestCandidate(id)
	if candidate.IsZero() || candidate.Address == self.Address {
		return self, 1, nil
	}
	node, hops, err := n.rpc.FindSuccessor(ctx, candidate.Address, id)
	if err != nil {
		return overlay.RemoteNode{}, hops + 1, err
	}
	return node, hops + 1, nil
}

func (n *Node) closestCandidate(id overlay.Identifier) overlay.RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.debruijn.IsZero() && n.debruijn.Address != n.self.Address && n.debruijn.ID.Between(n.self.ID, id) {
		return n.debruijn
	}
	for i := len(n.successors) - 1; i >= 0; i-- {
		candidate := n.successors[i]
		if candidate.IsZero() || candidate.Address == n.self.Address {
			continue
		}
		if candidate.ID.Between(n.self.ID, id) {
			return candidate
		}
	}
	return n.successor
}

// Notify informs node about potential predecessor.
func (n *Node) Notify(candidate overlay.RemoteNode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.predecessor.IsZero() || candidate.ID.Between(n.predecessor.ID, n.self.ID) {
		n.predecessor = candidate
	}
}

// Stabilize performs stabilization routine.
func (n *Node) Stabilize(ctx context.Context) error {
	succ := n.Successor()
	if succ.IsZero() {
		return errors.New("stabilize: no successor")
	}
	if succ.Address == n.self.Address {
		return nil
	}
	x, err := n.rpc.GetPredecessor(ctx, succ.Address)
	if err != nil {
		return fmt.Errorf("stabilize: get predecessor: %w", err)
	}
	if !x.IsZero() && x.ID.Between(n.self.ID, succ.ID) {
		n.mu.Lock()
		n.successor = x
		n.successors[0] = x
		n.mu.Unlock()
		succ = x
	}
	if err := n.rpc.Notify(ctx, succ.Address, n.self); err != nil {
		return fmt.Errorf("stabilize: notify successor: %w", err)
	}
	list, err := n.rpc.GetSuccessorList(ctx, succ.Address)
	if err != nil {
		return fmt.Errorf("stabilize: successor list: %w", err)
	}
	n.mu.Lock()
	n.successors[0] = succ
	for i := 1; i < len(n.successors); i++ {
		if i-1 < len(list) {
			n.successors[i] = list[i-1]
		}
	}
	n.mu.Unlock()
	n.refreshDebruijn(ctx)
	return nil
}

// CheckPredecessor verifies predecessor liveness.
func (n *Node) CheckPredecessor(ctx context.Context) {
	pred := n.Predecessor()
	if pred.IsZero() {
		return
	}
	if err := n.rpc.Ping(ctx, pred.Address); err != nil {
		n.mu.Lock()
		if n.predecessor.Address == pred.Address {
			n.predecessor = overlay.RemoteNode{}
		}
		n.mu.Unlock()
	}
}

// UpdateSuccessor handles successor failures.
func (n *Node) UpdateSuccessor(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.successor.Address == n.self.Address {
		return
	}
	for _, candidate := range n.successors {
		if candidate.IsZero() || candidate.Address == n.self.Address {
			continue
		}
		if err := n.rpc.Ping(ctx, candidate.Address); err != nil {
			continue
		}
		n.successor = candidate
		return
	}
	n.successor = n.self
}

// RunBackground runs maintenance loops.
func (n *Node) RunBackground(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go n.loop(ctx, n.cfg.StabilizeInterval, func(ctx context.Context) {
		if err := n.Stabilize(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			n.cfg.Logger.Warn("koorde stabilize error", slog.String("err", err.Error()))
			n.UpdateSuccessor(ctx)
		}
	})
	go n.loop(ctx, n.cfg.FixPointerInterval, func(ctx context.Context) {
		n.refreshDebruijn(ctx)
	})
	go n.loop(ctx, n.cfg.CheckPredecessor, func(ctx context.Context) {
		n.CheckPredecessor(ctx)
	})
	return cancel
}

func (n *Node) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			subCtx, cancel := context.WithTimeout(ctx, interval/2+500*time.Millisecond)
			fn(subCtx)
			cancel()
		}
	}
}

func (n *Node) refreshDebruijn(ctx context.Context) {
	succ := n.Successor()
	if succ.IsZero() || succ.Address == n.self.Address {
		n.mu.Lock()
		n.debruijn = n.self
		n.mu.Unlock()
		return
	}
	target := n.debruijnTarget()
	node, _, err := n.rpc.FindSuccessor(ctx, succ.Address, target)
	if err != nil || node.IsZero() {
		if err != nil {
			n.cfg.Logger.Debug("koorde debruijn refresh failed", slog.String("err", err.Error()))
		}
		return
	}
	n.mu.Lock()
	n.debruijn = node
	n.mu.Unlock()
}

func (n *Node) debruijnTarget() overlay.Identifier {
	return overlay.Identifier(uint64(n.self.ID) << 1)
}

// RegisterHandlers attaches HTTP handlers.
func (n *Node) RegisterHandlers(mux *http.ServeMux) {
	RegisterHandlers(mux, n)
}
