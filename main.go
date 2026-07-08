package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnsmasq-web/internal/api"
)

func main() {
	conf := &api.Config{
		DnsmasqConf:   getEnv("DNSMASQ_CONF", "/etc/dnsmasq.conf"),
		DnsmasqLeases: getEnv("DNSMASQ_LEASES", "/var/lib/dnsmasq/dnsmasq.leases"),
		BackupDir:     getEnv("BACKUP_DIR", "/var/backups/dnsmasq-web"),
		Host:          getEnv("HOST", ""),
		Port:          getEnv("PORT", "8053"),
		TemplateDir:   getEnv("TEMPLATE_DIR", "./templates"),
		StaticDir:     getEnv("STATIC_DIR", "./static"),
		AuthPassword:  os.Getenv("AUTH_PASSWORD"), // non-empty → login required
		APIToken:      os.Getenv("API_TOKEN"),     // non-empty → Bearer auth for MCP/scripts
	}

	srv, err := api.NewServer(conf)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.Start(ctx)

	handler := srv.SetupRoutes()
	httpSrv := &http.Server{
		Addr:              conf.Host + ":" + conf.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second, // no Read/WriteTimeout: SSE streams are long-lived
	}

	// Graceful shutdown: let in-flight requests finish, but don't wait on
	// open SSE streams for more than a couple of seconds.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Println("\nShutting down...")
		cancel() // stop background producers (journalctl follower, watchers)
		sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer scancel()
		if err := httpSrv.Shutdown(sctx); err != nil {
			httpSrv.Close()
		}
	}()

	host := conf.Host
	if host == "" {
		host = "localhost"
	}
	fmt.Printf("🌐 dnsmasq-web running on http://%s:%s\n", host, conf.Port)
	fmt.Printf("   Config:  %s\n", conf.DnsmasqConf)
	fmt.Printf("   Leases:  %s\n", conf.DnsmasqLeases)
	fmt.Printf("   Backups: %s\n", conf.BackupDir)

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
