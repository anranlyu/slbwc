package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"slbwc/internal/node"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	if value == "" {
		return nil
	}
	*s = append(*s, value)
	return nil
}

func main() {
	var (
		address       = flag.String("address", "127.0.0.1:8080", "Public address (host:port) advertised to peers")
		bind          = flag.String("bind", "", "Local bind address (defaults to address)")
		id            = flag.String("id", "", "Optional node ID seed")
		mode          = flag.String("mode", string(node.ProxyMode), "Routing mode: proxy or redirect")
		replicas      = flag.Int("replicas", 3, "Replication factor")
		cacheCap      = flag.Int("cache-capacity", 512, "Cache capacity (entries)")
		defaultTTL    = flag.Duration("default-ttl", 5*time.Minute, "Default TTL when origin omits cache headers")
		janitorPeriod = flag.Duration("cache-janitor-period", 30*time.Second, "Background cache cleanup interval")
		originTimeout = flag.Duration("origin-timeout", 10*time.Second, "Timeout for origin fetches")
		seeds         stringSliceFlag
	)
	flag.Var(&seeds, "seed", "Seed node address (repeatable)")
	flag.Parse()

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)

	cfg := node.Config{
		ID:                 *id,
		Address:            *address,
		BindAddr:           *bind,
		Seeds:              append([]string(nil), seeds...),
		Mode:               node.Mode(*mode),
		ReplicationFactor:  *replicas,
		CacheCapacity:      *cacheCap,
		DefaultTTL:         *defaultTTL,
		CacheJanitorPeriod: *janitorPeriod,
		OriginTimeout:      *originTimeout,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	n, err := node.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to create node", slog.String("err", err.Error()))
		os.Exit(1)
	}

	if err := n.Start(ctx); err != nil {
		logger.Error("failed to start node", slog.String("err", err.Error()))
		os.Exit(1)
	}

	logger.Info("node started", slog.String("address", cfg.Address))

	<-ctx.Done()
	logger.Info("shutting down node")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := n.Stop(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
	logger.Info("node stopped gracefully")
	fmt.Println("bye")
}

