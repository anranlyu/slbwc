package koorde

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"slbwc/internal/overlay"
)

type stubRPC struct {
	lastAddr string
	findResp overlay.RemoteNode
	hops     int
}

func (s *stubRPC) FindSuccessor(ctx context.Context, address string, id overlay.Identifier) (overlay.RemoteNode, int, error) {
	s.lastAddr = address
	if !s.findResp.IsZero() {
		return s.findResp, s.hops, nil
	}
	return overlay.RemoteNode{ID: id, Address: "resolved"}, 1, nil
}

func (s *stubRPC) GetSuccessor(ctx context.Context, address string) (overlay.RemoteNode, error) {
	return overlay.RemoteNode{}, nil
}

func (s *stubRPC) GetPredecessor(ctx context.Context, address string) (overlay.RemoteNode, error) {
	return overlay.RemoteNode{}, nil
}

func (s *stubRPC) Notify(ctx context.Context, address string, candidate overlay.RemoteNode) error {
	return nil
}

func (s *stubRPC) Ping(ctx context.Context, address string) error {
	return nil
}

func (s *stubRPC) GetSuccessorList(ctx context.Context, address string) ([]overlay.RemoteNode, error) {
	return nil, nil
}

func TestKoordeFindSuccessorUsesDebruijnPointer(t *testing.T) {
	self := overlay.RemoteNode{ID: overlay.Identifier(10), Address: "self"}
	rpc := &stubRPC{}
	cfg := DefaultConfig()
	node := NewNode(self, rpc, cfg)

	node.mu.Lock()
	node.successor = overlay.RemoteNode{ID: overlay.Identifier(30), Address: "succ"}
	node.successors[0] = node.successor
	node.debruijn = overlay.RemoteNode{ID: overlay.Identifier(25), Address: "db"}
	node.mu.Unlock()

	target := overlay.Identifier(40)
	res, hops, err := node.FindSuccessor(context.Background(), target)
	if err != nil {
		t.Fatalf("FindSuccessor returned error: %v", err)
	}
	if hops != 2 {
		t.Fatalf("expected 2 hops (local + rpc), got %d", hops)
	}
	if rpc.lastAddr != "db" {
		t.Fatalf("expected RPC to debruijn node, got %s", rpc.lastAddr)
	}
	if res.ID != target {
		t.Fatalf("expected resolved node ID %v, got %v", target, res.ID)
	}
}

func TestKoordeRefreshDebruijnUpdatesPointer(t *testing.T) {
	self := overlay.RemoteNode{ID: overlay.Identifier(5), Address: "self"}
	rpc := &stubRPC{
		findResp: overlay.RemoteNode{ID: overlay.Identifier(12), Address: "db2"},
		hops:     1,
	}
	cfg := DefaultConfig()
	cfg.FixPointerInterval = time.Second
	node := NewNode(self, rpc, cfg)

	node.mu.Lock()
	node.successor = overlay.RemoteNode{ID: overlay.Identifier(8), Address: "succ"}
	node.successors[0] = node.successor
	node.mu.Unlock()

	node.refreshDebruijn(context.Background())

	node.mu.RLock()
	defer node.mu.RUnlock()
	if node.debruijn.Address != "db2" {
		t.Fatalf("expected debruijn pointer to update, got %s", node.debruijn.Address)
	}
	if rpc.lastAddr != "succ" {
		t.Fatalf("expected RPC to successor, got %s", rpc.lastAddr)
	}
}

func TestKoordeLookupAcrossNodes(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.SuccessorListSize = 4

	net := newFakeNetwork()

	nodeA := koordeNodeWithID("nodeA", 10, net, cfg)
	net.Register(nodeA)

	nodeB := koordeNodeWithID("nodeB", 40, net, cfg)
	net.Register(nodeB)

	nodeC := koordeNodeWithID("nodeC", 70, net, cfg)
	net.Register(nodeC)

	configureNode(nodeA, nodeB.Self(), nodeC.Self(), nodeB.Self(), []overlay.RemoteNode{nodeB.Self(), nodeC.Self()})
	configureNode(nodeB, nodeC.Self(), nodeA.Self(), nodeA.Self(), []overlay.RemoteNode{nodeC.Self(), nodeA.Self()})
	configureNode(nodeC, nodeA.Self(), nodeB.Self(), nodeA.Self(), []overlay.RemoteNode{nodeA.Self(), nodeB.Self()})

	checkLookup := func(from *Node, key overlay.Identifier, want overlay.RemoteNode) {
		got, hops, err := from.FindSuccessor(ctx, key)
		if err != nil {
			t.Fatalf("FindSuccessor(%s) error: %v", from.self.Address, err)
		}
		if got.Address != want.Address {
			t.Fatalf("FindSuccessor(%s) = %s, want %s", from.self.Address, got.Address, want.Address)
		}
		if hops < 1 {
			t.Fatalf("expected at least 1 hop, got %d", hops)
		}
	}

	checkLookup(nodeA, overlay.Identifier(15), nodeB.Self())
	checkLookup(nodeB, overlay.Identifier(65), nodeC.Self())
	checkLookup(nodeC, overlay.Identifier(75), nodeA.Self())
}

func TestKoordeLookupLargeRing(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.SuccessorListSize = 6

	net := newFakeNetwork()
	total := 40
	nodes := make([]*Node, total)
	for i := 0; i < total; i++ {
		id := uint64(100 + i*131)
		address := fmt.Sprintf("node%02d", i)
		node := koordeNodeWithID(address, id, net, cfg)
		nodes[i] = node
		net.Register(node)
	}

	sorted := append([]*Node(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Self().ID < sorted[j].Self().ID
	})

	for idx, node := range sorted {
		successor := sorted[(idx+1)%total].Self()
		predecessor := sorted[(idx-1+total)%total].Self()
		successors := make([]overlay.RemoteNode, cfg.SuccessorListSize)
		for s := 0; s < cfg.SuccessorListSize; s++ {
			successors[s] = sorted[(idx+1+s)%total].Self()
		}
		configureNode(node, successor, predecessor, overlay.RemoteNode{}, successors)
	}

	for _, node := range sorted {
		node.refreshDebruijn(ctx)
	}

	verify := func(start *Node, key overlay.Identifier, want overlay.RemoteNode) {
		got, hops, err := start.FindSuccessor(ctx, key)
		if err != nil {
			t.Fatalf("FindSuccessor(%s -> %s) error: %v", start.Self().Address, key.String(), err)
		}
		if got.Address != want.Address {
			t.Fatalf("FindSuccessor(%s -> %s) = %s, want %s", start.Self().Address, key.String(), got.Address, want.Address)
		}
		if hops < 1 {
			t.Fatalf("expected at least 1 hop, got %d", hops)
		}
	}

	for _, startNode := range sorted {
		for targetIdx, targetNode := range sorted {
			prev := sorted[(targetIdx-1+total)%total].Self()
			target := targetNode.Self()
			keyMid := midpoint(prev.ID, target.ID)
			verify(startNode, keyMid, target)
		}
	}
}

func configureNode(node *Node, successor, predecessor, debruijn overlay.RemoteNode, successors []overlay.RemoteNode) {
	node.mu.Lock()
	defer node.mu.Unlock()
	node.successor = successor
	node.predecessor = predecessor
	node.debruijn = debruijn
	for i := range node.successors {
		if i < len(successors) {
			node.successors[i] = successors[i]
		} else {
			node.successors[i] = overlay.RemoteNode{}
		}
	}
	if len(successors) == 0 && len(node.successors) > 0 {
		node.successors[0] = successor
	}
}

type fakeNetwork struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{
		nodes: make(map[string]*Node),
	}
}

func (n *fakeNetwork) Register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.Self().Address] = node
}

func (n *fakeNetwork) get(address string) (*Node, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	node, ok := n.nodes[address]
	if !ok {
		return nil, fmt.Errorf("unknown node %s", address)
	}
	return node, nil
}

type fakeClient struct {
	net *fakeNetwork
}

func (c *fakeClient) target(address string) (*Node, error) {
	return c.net.get(address)
}

func (c *fakeClient) FindSuccessor(ctx context.Context, address string, id overlay.Identifier) (overlay.RemoteNode, int, error) {
	node, err := c.target(address)
	if err != nil {
		return overlay.RemoteNode{}, 0, err
	}
	return node.FindSuccessor(ctx, id)
}

func (c *fakeClient) GetSuccessor(ctx context.Context, address string) (overlay.RemoteNode, error) {
	node, err := c.target(address)
	if err != nil {
		return overlay.RemoteNode{}, err
	}
	return node.Successor(), nil
}

func (c *fakeClient) GetPredecessor(ctx context.Context, address string) (overlay.RemoteNode, error) {
	node, err := c.target(address)
	if err != nil {
		return overlay.RemoteNode{}, err
	}
	return node.Predecessor(), nil
}

func (c *fakeClient) Notify(ctx context.Context, address string, candidate overlay.RemoteNode) error {
	node, err := c.target(address)
	if err != nil {
		return err
	}
	node.Notify(candidate)
	return nil
}

func (c *fakeClient) Ping(ctx context.Context, address string) error {
	_, err := c.target(address)
	return err
}

func (c *fakeClient) GetSuccessorList(ctx context.Context, address string) ([]overlay.RemoteNode, error) {
	node, err := c.target(address)
	if err != nil {
		return nil, err
	}
	return node.SuccessorList(), nil
}

func koordeNodeWithID(address string, id uint64, net *fakeNetwork, cfg Config) *Node {
	self := overlay.RemoteNode{
		ID:      overlay.Identifier(id),
		Address: address,
	}
	client := &fakeClient{net: net}
	return NewNode(self, client, cfg)
}

func midpoint(prev, curr overlay.Identifier) overlay.Identifier {
	if prev < curr {
		return overlay.Identifier((uint64(prev) + uint64(curr)) / 2)
	}
	span := (^uint64(0) - uint64(prev)) + uint64(curr)
	mid := uint64(prev) + span/2
	return overlay.Identifier(mid)
}
