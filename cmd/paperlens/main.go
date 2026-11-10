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
	agent := &copilot.Agent{Path: c.CLIPath, Token: c.CopilotToken, Model: c.Model, Effort: c.ReasoningEffort, Root: c.RuntimeDir}
	if err := agent.Prepare(); err != nil {
		return err
	}
	defer func() {
		if err := agent.Close(); err != nil {
			slog.Error("runtime ownership release failed")
		}
	}()
	if c.AI && !c.Demo {
		warm, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := agent.Warm(warm); err != nil {
			slog.Warn("copilot package cache warm-up failed; searches will unpack the CLI themselves")
		}
		cancel()
	}
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
	engine := &search.Engine{API: api, ExcludedTags: c.ExcludedTags, Limits: search.Limits{Searches: c.MaxSearches, Candidates: c.MaxCandidates, Excerpts: c.MaxExcerpts, Characters: c.ExcerptChars}}
	if c.AI {
		vocab, err := api.Metadata(ctx)
		if err != nil {
			return err
		}
		if engine.ValidatePolicy(vocab) != nil {
			slog.Warn("AI unavailable at startup", "reason", "exclusion_policy_unresolved")
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
	app.BeginShutdown()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return errors.Join(err, srv.Close())
	}
	return nil
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
	target, err := healthTarget(listen)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
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

func healthTarget(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
