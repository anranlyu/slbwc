package overlay

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	// IdentifierBits is the number of bits for the overlay ring.
	IdentifierBits  = 64
	identifierBytes = IdentifierBits / 8
)

// Identifier represents a position on the overlay ring.
type Identifier uint64

// HashKey hashes a key into the ring identifier space.
func HashKey(key string) Identifier {
	sum := sha1.Sum([]byte(key))
	return Identifier(binary.BigEndian.Uint64(sum[:identifierBytes]))
}

// String returns hex representation.
func (id Identifier) String() string {
	return fmt.Sprintf("%016x", uint64(id))
}

// Between reports whether id is strictly between start (exclusive) and end (inclusive).
func (id Identifier) Between(start, end Identifier) bool {
	if start == end {
		return id != start
	}
	if start < end {
		return id > start && id <= end
	}
	return id > start || id <= end
}

// BetweenLeftInclusive reports whether id is between start (inclusive) and end (exclusive).
func (id Identifier) BetweenLeftInclusive(start, end Identifier) bool {
	if start == end {
		return id != end
	}
	if start < end {
		return id >= start && id < end
	}
	return id >= start || id < end
}

// RemoteNode describes a node in the overlay.
type RemoteNode struct {
	ID      Identifier `json:"id"`
	Address string     `json:"address"`
}

// IsZero reports if RemoteNode has zero-value fields.
func (n RemoteNode) IsZero() bool {
	return n.Address == "" && n.ID == 0
}

// String formats RemoteNode.
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

// MustParseRemote validates address and constructs a RemoteNode.
func MustParseRemote(id Identifier, address string) RemoteNode {
	if _, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/", address), nil); err != nil {
		panic(fmt.Sprintf("invalid address %s: %v", address, err))
	}
	return RemoteNode{ID: id, Address: address}
}
