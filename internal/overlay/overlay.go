package overlay

import (
	"context"
	"net/http"
)

// Routing defines the behaviour required by the node layer for an overlay implementation.
type Routing interface {
	Self() RemoteNode
	Join(ctx context.Context, seed RemoteNode) error
	FindSuccessor(ctx context.Context, id Identifier) (RemoteNode, int, error)
	Successor() RemoteNode
	SuccessorList() []RemoteNode
	RunBackground(ctx context.Context) context.CancelFunc
	RegisterHandlers(mux *http.ServeMux)
}
