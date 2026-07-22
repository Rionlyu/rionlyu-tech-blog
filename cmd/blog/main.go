package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	contentfiles "github.com/Rionlyu/rionlyu-tech-blog/content"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/config"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/contact"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
	bloghttp "github.com/Rionlyu/rionlyu-tech-blog/internal/httpserver"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/render"
	"github.com/Rionlyu/rionlyu-tech-blog/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("blog stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := newLogger(cfg.IsProduction())
	slog.SetDefault(logger)

	store, err := content.Load(contentfiles.Files)
	if err != nil {
		return fmt.Errorf("load content: %w", err)
	}
	renderer, err := render.New(web.Files)
	if err != nil {
		return fmt.Errorf("load renderer: %w", err)
	}
	sender := contactSender(cfg)
	limiter := contact.NewLimiter(cfg.ContactRateLimit, cfg.ContactRateWindow, time.Now)
	globalLimiter := contact.NewLimiter(cfg.ContactGlobalLimit, cfg.ContactRateWindow, time.Now)
	application, err := bloghttp.New(bloghttp.Dependencies{
		Config:        cfg,
		Content:       store,
		Renderer:      renderer,
		Sender:        sender,
		Limiter:       limiter,
		GlobalLimiter: globalLimiter,
		Logger:        logger,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           application.Handler(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr, "environment", cfg.Environment)
		serveErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown started")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP after shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(production bool) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if production {
		return slog.New(slog.NewJSONHandler(os.Stdout, options))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, options))
}

func contactSender(cfg config.Config) contact.Sender {
	switch cfg.ContactProvider {
	case "webhook":
		client := &http.Client{
			Timeout: cfg.ContactTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		return contact.NewWebhookSender(client, cfg.ContactWebhookURL, cfg.ContactWebhookToken)
	case "disabled":
		return contact.DisabledSender{}
	default:
		return contact.DevNullSender{}
	}
}
