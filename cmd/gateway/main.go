package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"he-gateway/internal/autocert"
	"he-gateway/internal/cli"
	"he-gateway/internal/hardware"
	"he-gateway/internal/websocket"
	"he-gateway/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("gateway stopped", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := cli.ParseArgs(args)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.JSONLogs)
	slog.SetDefault(logger)
	if cfg.InstallCA != "" {
		return autocert.InstallElevated(cfg.InstallCA)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err = resolveTLS(ctx, cfg, logger)
	if err != nil {
		return err
	}

	hub := websocket.NewHub(logger)
	hwCfg := hardware.DefaultConfig()
	hwCfg.BaudRate = cfg.Baud
	hwCfg.Pulse = cfg.Pulse
	if cfg.OpenCmd != nil {
		hwCfg.OpenCommand = cfg.OpenCmd
	}
	if cfg.CloseCmd != nil {
		hwCfg.CloseCommand = cfg.CloseCmd
	}
	hwCfg.OnRead = func(ev hardware.ReadEvent) {
		logger.Info("hardware read", "device_id", ev.DeviceID, "data", ev.Data)
		hub.Broadcast(websocket.Outbound{
			Event:    "hardware_read",
			DeviceID: ev.DeviceID,
			Data:     ev.Data,
		})
	}
	mgr := hardware.NewManager(hardware.SystemOpener{}, hwCfg, logger)
	defer func() {
		hub.Close()
		if err := mgr.Close(); err != nil {
			logger.Error("close hardware", "err", err)
		}
	}()

	for _, id := range cfg.Devices {
		if err := mgr.StartListen(ctx, id); err != nil {
			logger.Error("listen on startup", "device_id", id, "err", err)
		}
	}

	srv := websocket.NewServer(hub, mgr, web.IndexHTML, logger)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	dashboard := dashboardURL(cfg)
	logger.Info("gateway listening", "addr", cfg.Addr, "dashboard", dashboard)
	if err := autocert.OpenBrowser(dashboard); err != nil {
		logger.Error("open dashboard", "err", err)
	}
	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if cfg.CertFile != "" {
			serveErr = httpSrv.ServeTLS(ln, cfg.CertFile, cfg.KeyFile)
		} else {
			serveErr = httpSrv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

func resolveTLS(ctx context.Context, cfg cli.Config, logger *slog.Logger) (cli.Config, error) {
	if cfg.PlainHTTP || cfg.CertFile != "" {
		return cfg, nil
	}
	dir, err := autocert.DataDir()
	if err != nil {
		return cfg, err
	}
	certFile, keyFile, err := autocert.Ensure(dir, time.Now())
	if err != nil {
		return cfg, err
	}
	if err := autocert.Trust(ctx, filepath.Join(dir, autocert.CAName), autocert.SystemInstaller()); err != nil {
		logger.Error("install local ca", "err", err)
	}
	cfg.CertFile = certFile
	cfg.KeyFile = keyFile
	return cfg, nil
}

func dashboardURL(cfg cli.Config) string {
	scheme := "http"
	if cfg.CertFile != "" {
		scheme = "https"
	}
	return scheme + "://" + cfg.Addr
}

func newLogger(jsonLogs bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var handler slog.Handler
	if jsonLogs {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
