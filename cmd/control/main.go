package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
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
		repository = store.NewMemoryBootstrap()
	}
	if closeRepository != nil {
		defer func() {
			if err := closeRepository(); err != nil {
				logger.Error("database close failed", "error", err)
			}
		}()
	}
	var modelGateway control.ModelGateway
	if configuration.GatewayBaseURL != "" {
		modelGateway, err = gateway.NewModelsClient(configuration.GatewayBaseURL, configuration.GatewayToken)
		if err != nil {
			logger.Error("invalid gateway configuration", "error", err)
			os.Exit(1)
		}
	}
	service := control.NewService(repository, modelGateway)
	if configuration.LoginDirectoryBaseURL != "" {
		directory, err := gateway.NewLoginDirectoryClient(configuration.LoginDirectoryBaseURL, configuration.LoginDirectoryToken)
		if err != nil {
			logger.Error("invalid Login directory configuration", "error", err)
			os.Exit(1)
		}
		service.SetLoginDirectory(directory)
	}
	if configuration.GatewayAdminToken != "" {
		accountGateway, err := gateway.NewCenterClient(configuration.GatewayBaseURL, configuration.GatewayAdminToken)
		if err != nil {
			logger.Error("invalid center gateway configuration", "error", err)
			os.Exit(1)
		}
		service.SetGatewayAccounts(accountGateway)
	}
	sweepContext, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			operationContext, cancel := context.WithTimeout(sweepContext, 10*time.Second)
			if err := service.SweepModelExperienceRuns(operationContext); err != nil && sweepContext.Err() == nil {
				logger.Error("model experience cleanup failed", "error", err)
			}
			cancel()
			select {
			case <-sweepContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
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
	var stationServer *http.Server
	if configuration.StationMTLSAddress != "" {
		caPEM, err := os.ReadFile(configuration.StationMTLSCAFile)
		if err != nil {
			logger.Error("station CA unreadable", "error", err)
			os.Exit(1)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			logger.Error("station CA invalid")
			os.Exit(1)
		}
		if _, err := tls.LoadX509KeyPair(configuration.StationMTLSCertFile, configuration.StationMTLSKeyFile); err != nil {
			logger.Error("station TLS identity invalid", "error", err)
			os.Exit(1)
		}
		stationServer = &http.Server{
			Addr: configuration.StationMTLSAddress, Handler: handler.StationHandler(),
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool},
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: configuration.ReadTimeout,
			WriteTimeout: configuration.WriteTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
		}
	}

	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("control service started", "address", configuration.Address, "environment", configuration.Environment, "store", storeName)
		serverErrors <- server.ListenAndServe()
	}()
	if stationServer != nil {
		go func() {
			logger.Info("station mTLS interface started", "address", configuration.StationMTLSAddress)
			serverErrors <- stationServer.ListenAndServeTLS(configuration.StationMTLSCertFile, configuration.StationMTLSKeyFile)
		}()
	}

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
	if stationServer != nil {
		if err := stationServer.Shutdown(shutdownContext); err != nil {
			logger.Error("station interface shutdown failed", "error", err)
			_ = stationServer.Close()
			os.Exit(1)
		}
	}
	logger.Info("control service stopped")
}
