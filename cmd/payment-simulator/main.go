package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fssrepository/myscoutee-payment-simulator/internal/simulator"
)

func main() {
	config, err := simulator.ConfigFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := simulator.CheckHealth(config.ListenAddress); err != nil {
			log.Fatal(err)
		}
		return
	}

	handler, err := simulator.New(config)
	if err != nil {
		log.Fatal(err)
	}
	defer handler.Close()
	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("MyScoutee payment simulator listening on %s (test-only, live keys rejected)", config.ListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	log.SetPrefix(strings.TrimSpace("payment-simulator "))
}
