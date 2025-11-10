package chord

import "slbwc/internal/overlay"

const (
	// IdentifierBits is maintained for backward compatibility.
	IdentifierBits = overlay.IdentifierBits
)

// Identifier aliases overlay.Identifier for existing code.
type Identifier = overlay.Identifier

// HashKey proxies to overlay.HashKey.
func HashKey(key string) Identifier {
	return overlay.HashKey(key)
}
