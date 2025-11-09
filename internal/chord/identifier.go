package chord

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

const (
	// IdentifierBits is the number of bits for the Chord ring.
	IdentifierBits = 64
	// identifierBytes is derived from IdentifierBits.
	identifierBytes = IdentifierBits / 8
)

// Identifier represents a point on the ring.
type Identifier uint64

// HashKey hashes an arbitrary string key into a ring identifier.
func HashKey(key string) Identifier {
	sum := sha1.Sum([]byte(key))
	return Identifier(binary.BigEndian.Uint64(sum[:identifierBytes]))
}

// String returns the hex formatted identifier.
func (id Identifier) String() string {
	return fmt.Sprintf("%016x", uint64(id))
}

// Between returns true when id is strictly between start (exclusive) and end (inclusive) on the ring.
func (id Identifier) Between(start, end Identifier) bool {
	if start == end {
		return id != start
	}
	if start < end {
		return id > start && id <= end
	}
	return id > start || id <= end
}

// BetweenLeftInclusive returns true when id is between start (inclusive) and end (exclusive).
func (id Identifier) BetweenLeftInclusive(start, end Identifier) bool {
	if start == end {
		return id != end
	}
	if start < end {
		return id >= start && id < end
	}
	return id >= start || id < end
}

