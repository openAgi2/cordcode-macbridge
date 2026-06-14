package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cccode-relay/internal/relay"
)

func main() {
	listen := flag.String("listen", envOr("RELAY_LISTEN_ADDR", "127.0.0.1:8780"), "HTTP listen address")
	database := flag.String("db", envOr("RELAY_DB_PATH", "/var/lib/cccode-relay/relay.db"), "SQLite database path")
	endpoint := flag.String("public-endpoint", envOr("RELAY_PUBLIC_ENDPOINT", "ws://127.0.0.1:8780"), "public WebSocket endpoint")
	tokenHash := flag.String("provision-token-sha256", envOr("RELAY_PROVISION_TOKEN_SHA256", ""), "SHA-256 hex digest of provisioning token")
	mailboxTTL := flag.Duration("mailbox-ttl", durationOr("RELAY_MAILBOX_TTL", 24*time.Hour), "offline mailbox TTL")
	maxMailbox := flag.Int64("max-mailbox-bytes", int64Or("RELAY_MAX_MAILBOX_BYTES", 50<<20), "maximum pending mailbox bytes per device")
	maxFrame := flag.Int64("max-frame-bytes", int64Or("RELAY_MAX_FRAME_BYTES", 2<<20), "maximum WebSocket envelope bytes")
	rateLimit := flag.Int("rate-limit-per-minute", intOr("RELAY_RATE_LIMIT_PER_MINUTE", 120), "route registrations allowed per IP per minute")
	activationRateLimit := flag.Int("activation-rate-limit-per-minute", intOr("RELAY_ACTIVATION_RATE_LIMIT_PER_MINUTE", 6), "self-service activation requests allowed per IP per minute")
	flag.Parse()

	digest, err := relay.ProvisionTokenDigestFromHex(*tokenHash)
	if err != nil {
		slog.Error("relay startup rejected", "error", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(*database), 0o750); err != nil {
		slog.Error("create relay data directory", "error", err)
		os.Exit(1)
	}
	store, err := relay.OpenStore(*database)
	if err != nil {
		slog.Error("open relay store", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	server, err := relay.NewServer(store, relay.Config{
		PublicEndpoint:               *endpoint,
		ProvisionTokenDigest:         digest,
		MailboxTTL:                   *mailboxTTL,
		MaxMailboxBytes:              *maxMailbox,
		MaxFrameBytes:                *maxFrame,
		RateLimitPerMinute:           *rateLimit,
		ActivationRateLimitPerMinute: *activationRateLimit,
	}, slog.Default())
	if err != nil {
		slog.Error("configure relay server", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go cleanupMailbox(ctx, store)
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
	}()
	slog.Info("relay listening", "address", *listen, "endpoint", *endpoint)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("relay terminated", "error", err)
		os.Exit(1)
	}
}

func cleanupMailbox(ctx context.Context, store *relay.Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			count, err := store.ExpireFrames(ctx, now)
			if err != nil {
				slog.Warn("mailbox cleanup failed", "error", err)
			} else if count > 0 {
				slog.Info("mailbox frames removed", "count", count)
			}
		}
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationOr(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		panic(fmt.Sprintf("invalid %s: %v", name, err))
	}
	return parsed
}

func int64Or(name string, fallback int64) int64 {
	var parsed int64
	if value := os.Getenv(name); value != "" {
		if _, err := fmt.Sscan(value, &parsed); err != nil {
			panic(fmt.Sprintf("invalid %s: %v", name, err))
		}
		return parsed
	}
	return fallback
}

func intOr(name string, fallback int) int {
	return int(int64Or(name, int64(fallback)))
}
