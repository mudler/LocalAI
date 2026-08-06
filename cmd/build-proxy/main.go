package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/buildproxy"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18080", "proxy listen address")
	output := flag.String("output", ".cache/build-proxy", "telemetry directory")
	flag.Parse()
	if err := run(*listen, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(listen, output string) error {
	recorder, err := buildproxy.NewRecorder(filepath.Join(output, "events.jsonl"))
	if err != nil {
		return err
	}
	defer func() { _ = recorder.Close() }()
	proxyHandler := buildproxy.NewHandler(buildproxy.Options{Recorder: recorder})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHandler(w, r, r.URL.Host) })
	server, err := buildproxy.NewServer(listen, filepath.Join(output, "ca"), handler, recorder)
	if err != nil {
		return err
	}
	if err := server.Start(); err != nil {
		return err
	}
	fmt.Printf("proxy=http://%s\nca=%s\n", server.Addr(), server.CAPath())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		return err
	}
	return recorder.WriteSummary(filepath.Join(output, "summary.json"))
}
