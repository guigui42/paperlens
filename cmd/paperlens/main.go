package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guigui42/paperlens/internal/config"
	"github.com/guigui42/paperlens/internal/copilot"
	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
	"github.com/guigui42/paperlens/internal/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "runtime-check" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := copilot.Smoke(ctx, os.Getenv("COPILOT_CLI_PATH")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("restricted SDK/runtime handshake and cleanup passed; no model inference")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "health" {
		if err := health(); err != nil {
			fmt.Fprintln(os.Stderr, "health check failed")
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("Paperlens stopped", "error", err.Error())
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var api paperless.API
	if c.Demo {
		d := demo.New()
		d.Delay = 300 * time.Millisecond
		api = d
	} else {
		client, err := paperless.New(c.PaperlessURL, c.PaperlessToken)
		if err != nil {
			return err
		}
		probe, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Probe(probe)
		cancel()
		if err != nil {
			return err
		}
		api = client
	}
	engine := &search.Engine{API: api, Limits: search.Limits{Searches: c.MaxSearches, Candidates: c.MaxCandidates, Excerpts: c.MaxExcerpts, Characters: c.ExcerptChars}}
	if c.AI {
		agent := &copilot.Agent{Path: c.CLIPath, Token: c.CopilotToken, Model: c.Model, Root: c.RuntimeDir}
		if err := agent.Prepare(); err != nil {
			return err
		}
		engine.Agent = agent
	}
	app, err := web.New(c, api, engine)
	if err != nil {
		return err
	}
	defer app.Close()
	srv := &http.Server{Addr: c.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	slog.Info("Paperlens listening", "demo", c.Demo, "ai", c.AI)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
		return nil
	case <-ctx.Done():
	}
	app.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
func health() error {
	origin := os.Getenv("PAPERLENS_ORIGIN")
	if origin == "" {
		origin = "http://localhost:8080"
	}
	u, err := url.Parse(origin)
	if err != nil {
		return err
	}
	listen := os.Getenv("PAPERLENS_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return err
	}
	req.Host = u.Host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("unhealthy")
	}
	return nil
}
