package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	configuration, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	var repository store.Repository
	var closeRepository func() error
	storeName := "memory"
	if configuration.DatabaseURL != "" {
		postgresRepository, err := store.OpenPostgres(
			configuration.DatabaseURL,
			configuration.DatabaseMaxOpen,
			configuration.DatabaseMaxIdle,
			time.Now().UTC(),
		)
		if err != nil {
			logger.Error("database initialization failed", "error", err)
			os.Exit(1)
		}
		repository = postgresRepository
		closeRepository = postgresRepository.Close
		storeName = "postgres"
	} else {
		repository = store.NewMemorySeeded(time.Now().UTC())
	}
	if closeRepository != nil {
		defer func() {
			if err := closeRepository(); err != nil {
				logger.Error("database close failed", "error", err)
			}
		}()
	}
	service := control.NewService(repository)
	handler := httpapi.New(configuration, service, logger)

	server := &http.Server{
		Addr:              configuration.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       configuration.ReadTimeout,
		WriteTimeout:      configuration.WriteTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("control service started", "address", configuration.Address, "environment", configuration.Environment, "store", storeName)
		serverErrors <- server.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case signal := <-stop:
		logger.Info("shutdown requested", "signal", signal.String())
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), configuration.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		_ = server.Close()
		os.Exit(1)
	}
	logger.Info("control service stopped")
}
