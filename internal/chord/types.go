package chord

import (
	"encoding/json"
	"fmt"
)

// RemoteNode represents a Chord node accessible via HTTP.
type RemoteNode struct {
	ID      Identifier `json:"id"`
	Address string     `json:"address"`
}

// IsZero reports whether the remote node has zero values.
func (n RemoteNode) IsZero() bool {
	return n.Address == "" && n.ID == 0
}

// String formats the remote node.
func (n RemoteNode) String() string {
	return fmt.Sprintf("%s@%s", n.ID, n.Address)
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (n RemoteNode) MarshalBinary() ([]byte, error) {
	return json.Marshal(n)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (n *RemoteNode) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, n)
}

