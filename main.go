package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
		Addr:    conf.Host + ":" + conf.Port,
		Handler: handler,
	}

	// Graceful shutdown
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Println("\nShutting down...")
		httpSrv.Close()
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
