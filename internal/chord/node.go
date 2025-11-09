package chord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/bits"
	"net/http"
	"sync"
	"time"
)

// RPCClient defines the operations required to communicate with remote Chord nodes.
type RPCClient interface {
	FindSuccessor(ctx context.Context, address string, id Identifier) (RemoteNode, int, error)
	GetSuccessor(ctx context.Context, address string) (RemoteNode, error)
	GetPredecessor(ctx context.Context, address string) (RemoteNode, error)
	Notify(ctx context.Context, address string, candidate RemoteNode) error
	Ping(ctx context.Context, address string) error
	GetSuccessorList(ctx context.Context, address string) ([]RemoteNode, error)
}

// Config contains Chord maintenance configuration.
type Config struct {
	StabilizeInterval time.Duration
	FixFingerInterval time.Duration
	CheckPredecessor  time.Duration
	SuccessorListSize int
	Logger            *slog.Logger
}

// DefaultConfig returns Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		StabilizeInterval: 2 * time.Second,
		FixFingerInterval: 1500 * time.Millisecond,
		CheckPredecessor:  4 * time.Second,
		SuccessorListSize: 4,
		Logger:            slog.Default(),
	}
}

// Node implements the Chord overlay logic for a single node.
type Node struct {
	mu sync.RWMutex

	self RemoteNode

	successor    RemoteNode
	predecessor  RemoteNode
	finger       []RemoteNode
	successors   []RemoteNode
	nextFixIndex int

	cfg Config
	rpc RPCClient
}

// NewNode creates a Node with given config and RPC implementation.
func NewNode(self RemoteNode, rpc RPCClient, cfg Config) *Node {
	if cfg.SuccessorListSize <= 0 {
		cfg.SuccessorListSize = 4
	}
	fingers := make([]RemoteNode, IdentifierBits)
	for i := range fingers {
		fingers[i] = self
	}
	successorList := make([]RemoteNode, cfg.SuccessorListSize)
	for i := range successorList {
		successorList[i] = self
	}
	return &Node{
		self:       self,
		successor:  self,
		predecessor: RemoteNode{},
		finger:     fingers,
		successors: successorList,
		cfg:        cfg,
		rpc:        rpc,
	}
}

// Self returns the local remote node information.
func (n *Node) Self() RemoteNode {
	return n.self
}

// Successor returns the current successor.
func (n *Node) Successor() RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.successor
}

// SuccessorList returns the current successor list.
func (n *Node) SuccessorList() []RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]RemoteNode, 0, len(n.successors))
	for _, succ := range n.successors {
		if !succ.IsZero() {
			out = append(out, succ)
		}
	}
	return out
}

// Predecessor returns current predecessor.
func (n *Node) Predecessor() RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.predecessor
}

// SetPredecessor updates the predecessor.
func (n *Node) SetPredecessor(node RemoteNode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.predecessor = node
}

// Join initialises the node by contacting an existing node in the ring.
func (n *Node) Join(ctx context.Context, known RemoteNode) error {
	if known.IsZero() || known.Address == n.self.Address {
		n.mu.Lock()
		n.successor = n.self
		for i := range n.successors {
			n.successors[i] = n.self
		}
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
func (n *Node) FindSuccessor(ctx context.Context, id Identifier) (RemoteNode, int, error) {
	n.mu.RLock()
	succ := n.successor
	self := n.self
	n.mu.RUnlock()

	if id.BetweenLeftInclusive(self.ID, succ.ID) {
		return succ, 1, nil
	}

	cpn := n.closestPrecedingNode(id)
	if cpn.Address == self.Address {
		return self, 1, nil
	}
	node, hops, err := n.rpc.FindSuccessor(ctx, cpn.Address, id)
	if err != nil {
		return RemoteNode{}, hops + 1, err
	}
	return node, hops + 1, nil
}

// closestPrecedingNode returns the best-known finger preceding id.
func (n *Node) closestPrecedingNode(id Identifier) RemoteNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for i := len(n.finger) - 1; i >= 0; i-- {
		f := n.finger[i]
		if f.IsZero() {
			continue
		}
		if f.ID == n.self.ID {
			continue
		}
		if f.ID.Between(n.self.ID, id) {
			return f
		}
	}
	return n.successor
}

// Notify informs the node about a potential predecessor.
func (n *Node) Notify(candidate RemoteNode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.predecessor.IsZero() || candidate.ID.Between(n.predecessor.ID, n.self.ID) {
		n.predecessor = candidate
	}
}

// Stabilize performs the Chord stabilization routine.
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
	// update successor list
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
	return nil
}

// FixNextFinger updates a single finger each invocation.
func (n *Node) FixNextFinger(ctx context.Context) error {
	index := n.nextFingerIndex()
	defer func() {
		n.mu.Lock()
		n.nextFixIndex = (n.nextFixIndex + 1) % IdentifierBits
		n.mu.Unlock()
	}()

	offset := Identifier(1 << uint(index))
	id := Identifier(uint64(n.self.ID) + uint64(offset))
	succ, _, err := n.FindSuccessor(ctx, id)
	if err != nil {
		return fmt.Errorf("fix finger: %w", err)
	}
	n.mu.Lock()
	n.finger[index] = succ
	n.mu.Unlock()
	return nil
}

func (n *Node) nextFingerIndex() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nextFixIndex % IdentifierBits
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
			n.predecessor = RemoteNode{}
		}
		n.mu.Unlock()
	}
}

// UpdateSuccessor handles successor failure using the successor list.
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

// RunBackground starts stabilization loops and returns a function to stop them.
func (n *Node) RunBackground(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go n.loop(ctx, n.cfg.StabilizeInterval, func(ctx context.Context) {
		if err := n.Stabilize(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			n.cfg.Logger.Warn("stabilize error", slog.String("err", err.Error()))
			n.UpdateSuccessor(ctx)
		}
	})
	go n.loop(ctx, n.cfg.FixFingerInterval, func(ctx context.Context) {
		if err := n.FixNextFinger(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			n.cfg.Logger.Warn("fix finger error", slog.String("err", err.Error()))
		}
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

// HopEstimate returns log2 of finger table size as maximum hops.
func HopEstimate() int {
	return bits.Len64(uint64(IdentifierBits))
}

// MustParseRemote constructs RemoteNode and panics on invalid address.
func MustParseRemote(id Identifier, address string) RemoteNode {
	if _, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/", address), nil); err != nil {
		panic(fmt.Sprintf("invalid address %s: %v", address, err))
	}
	return RemoteNode{ID: id, Address: address}
}

