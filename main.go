package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML config file")
	addr := flag.String("addr", "", "Override listen address (host:port)")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	listenAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	if *addr != "" {
		listenAddr = *addr
	}

	// Initialize the monitor engine.
	mon := NewMonitor(cfg)

	// Initialize the alert system.
	alerts := NewAlertSystem(cfg, mon)

	// Initialize the dashboard.
	dash := NewDashboard(mon, alerts)

	// Build the HTTP mux.
	mux := http.NewServeMux()
	dash.RegisterRoutes(mux)

	// Health endpoint for the monitor itself.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","service":"nexus-monitor","time":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	})

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Start the monitor loop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mon.Start(ctx)
	go alerts.Start(ctx)

	// Graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[startup] nexus-monitor listening on %s", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-done
	log.Println("[shutdown] received signal, shutting down gracefully...")
	alerts.Disconnect()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}

	log.Println("[shutdown] nexus-monitor stopped")
}
