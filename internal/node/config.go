package node

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidAddress indicates missing address for node.
	ErrInvalidAddress = errors.New("node: address must be set")
	// ErrInvalidMode indicates invalid routing mode.
	ErrInvalidMode = errors.New("node: invalid mode")
	// ErrInvalidOverlay indicates unsupported overlay type.
	ErrInvalidOverlay = errors.New("node: invalid overlay")
)

// Mode defines forwarding behaviour.
type Mode string

const (
	// ProxyMode proxies requests to owner.
	ProxyMode Mode = "proxy"
	// RedirectMode redirects clients to owner node.
	RedirectMode Mode = "redirect"
)

// Overlay identifies overlay algorithm.
type Overlay string

const (
	// OverlayChord selects the Chord overlay.
	OverlayChord Overlay = "chord"
	// OverlayKoorde selects the Koorde overlay.
	OverlayKoorde Overlay = "koorde"
)

// Config contains runtime configuration for a node instance.
type Config struct {
	ID                 string
	Address            string
	BindAddr           string
	Seeds              []string
	Overlay            Overlay
	Mode               Mode
	ReplicationFactor  int
	CacheCapacity      int
	DefaultTTL         time.Duration
	CacheJanitorPeriod time.Duration
	OriginTimeout      time.Duration
}

// Validate ensures configuration is acceptable.
func (c *Config) Validate() error {
	if c.Address == "" {
		return ErrInvalidAddress
	}
	if c.ReplicationFactor <= 0 {
		c.ReplicationFactor = 1
	}
	if c.CacheCapacity <= 0 {
		c.CacheCapacity = 512
	}
	if c.DefaultTTL <= 0 {
		c.DefaultTTL = 5 * time.Minute
	}
	if c.CacheJanitorPeriod <= 0 {
		c.CacheJanitorPeriod = 30 * time.Second
	}
	if c.OriginTimeout <= 0 {
		c.OriginTimeout = 10 * time.Second
	}
	if c.Overlay == "" {
		c.Overlay = OverlayChord
	}
	switch c.Overlay {
	case OverlayChord, OverlayKoorde:
	default:
		return fmt.Errorf("%w: %s", ErrInvalidOverlay, c.Overlay)
	}
	if c.Mode == "" {
		c.Mode = ProxyMode
	}
	switch c.Mode {
	case ProxyMode, RedirectMode:
	default:
		return fmt.Errorf("%w: %s", ErrInvalidMode, c.Mode)
	}
	return nil
}
