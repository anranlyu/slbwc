package node

import "testing"

func TestConfigValidateSetsDefaults(t *testing.T) {
	cfg := Config{
		Address: "127.0.0.1:8080",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Overlay != OverlayChord {
		t.Fatalf("expected default overlay %q, got %q", OverlayChord, cfg.Overlay)
	}
	if cfg.Mode != ProxyMode {
		t.Fatalf("expected default mode %q, got %q", ProxyMode, cfg.Mode)
	}
}

func TestConfigValidateRejectsInvalidOverlay(t *testing.T) {
	cfg := Config{
		Address: "127.0.0.1:8080",
		Overlay: Overlay("invalid"),
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for invalid overlay")
	}
}
