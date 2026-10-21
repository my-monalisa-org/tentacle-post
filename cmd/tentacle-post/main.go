package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/my-monalisa-org/tentacle-post/internal/inbox"
)

func main() {
	port := flag.Int("port", 8080, "loopback HTTP port (1-65535)")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		log.Fatal("port must be between 1 and 65535")
	}
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           inbox.New().Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finished <- server.Shutdown(shutdown)
	}()
	log.Printf("Tentacle Post sorting fictional mail at http://%s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	if err := <-finished; err != nil {
		log.Fatal(err)
	}
}
