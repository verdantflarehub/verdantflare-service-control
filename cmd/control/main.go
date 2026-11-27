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
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource"
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
		if configuration.SkillCatalogDemo {
			repository = store.NewMemorySkillCatalogDemo()
		} else {
			repository = store.NewMemoryBootstrap()
		}
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
	service.SetRequiredInstallGrantApps(configuration.RequiredInstallGrantApps)
	skillSource, err := skillsource.New(configuration.SkillSourceAPIURL, configuration.SkillSourceRepository, configuration.SkillSourceToken)
	if err != nil {
		logger.Error("invalid skill source configuration", "error", err)
		os.Exit(1)
	}
	service.SetSkillSource(skillSource)
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
	if configuration.GatewayModelOpsToken != "" {
		modelOpsGateway, err := gateway.NewModelOpsClient(configuration.GatewayBaseURL, configuration.GatewayModelOpsToken)
		if err != nil {
			logger.Error("invalid model operations gateway configuration", "error", err)
			os.Exit(1)
		}
		modelOpsGateway.SetCatalogToken(configuration.GatewayToken)
		service.SetModelOpsGateway(modelOpsGateway)
	}
	sweepContext, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		nextSurveySweep := time.Time{}
		for {
			operationContext, cancel := context.WithTimeout(sweepContext, 10*time.Second)
			if err := service.SweepModelExperienceRuns(operationContext); err != nil && sweepContext.Err() == nil {
				logger.Error("model experience cleanup failed", "error", err)
			}
			cancel()
			if !time.Now().Before(nextSurveySweep) {
				surveyContext, cancelSurvey := context.WithTimeout(sweepContext, 30*time.Second)
				err := service.SweepSurveyResponses(surveyContext)
				cancelSurvey()
				if err != nil {
					if sweepContext.Err() == nil {
						logger.Error("survey response cleanup failed", "error", err)
					}
					nextSurveySweep = time.Now().Add(time.Minute)
				} else {
					nextSurveySweep = time.Now().Add(time.Hour)
				}
			}
			select {
			case <-sweepContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	handler := httpapi.New(configuration, service, logger)
	var handoffServer *http.Server
	if configuration.AgentHandoff.Address != "" {
		cfg := configuration.AgentHandoff
		persistent, ok := repository.(*store.Postgres)
		if !ok {
			logger.Error("agent handoff requires persistent repository")
			os.Exit(1)
		}
		readyContext, cancelReady := context.WithTimeout(context.Background(), 5*time.Second)
		readyErr := persistent.AgentHandoffReady(readyContext, cfg.IdentityFloor, cfg.EpochFloor)
		cancelReady()
		if readyErr != nil {
			logger.Error("agent handoff revision and epoch must be initialized before enablement")
			os.Exit(1)
		}
		loginClient, err := gateway.NewLoginHandoffClient(cfg.LoginBaseURL, cfg.LoginToken)
		if err != nil {
			logger.Error("invalid handoff Login client configuration")
			os.Exit(1)
		}
		provider, err := httpapi.NewAgentHandoffProvider(persistent, loginClient, cfg.RegistryFile, cfg.Issuer)
		if err != nil {
			logger.Error("invalid handoff registry")
			os.Exit(1)
		}
		handler.SetAgentHandoff(provider)
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			logger.Error("handoff CA unreadable")
			os.Exit(1)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			logger.Error("handoff CA invalid")
			os.Exit(1)
		}
		if _, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile); err != nil {
			logger.Error("handoff TLS identity invalid")
			os.Exit(1)
		}
		handoffServer = &http.Server{Addr: cfg.Address, Handler: provider.InternalHandler(),
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool},
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
			IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	}

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

	var assistantServer *http.Server
	if configuration.AssistantMTLS.Address != "" {
		persistent, ok := repository.(*store.Postgres)
		if !ok {
			logger.Error("assistant identity requires persistent repository")
			os.Exit(1)
		}
		readyContext, cancelReady := context.WithTimeout(context.Background(), 5*time.Second)
		readyErr := persistent.AssistantIdentityReady(readyContext)
		cancelReady()
		if readyErr != nil {
			logger.Error("assistant identity revision must be initialized before enablement")
			os.Exit(1)
		}
		cfg := configuration.AssistantMTLS
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			logger.Error("assistant CA unreadable")
			os.Exit(1)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			logger.Error("assistant CA invalid")
			os.Exit(1)
		}
		if _, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile); err != nil {
			logger.Error("assistant TLS identity invalid")
			os.Exit(1)
		}
		assistantHandler, err := httpapi.NewAssistantIdentityHandler(persistent, cfg.ClientURI)
		if err != nil {
			logger.Error("assistant handler configuration invalid")
			os.Exit(1)
		}
		assistantServer = &http.Server{Addr: cfg.Address, Handler: assistantHandler,
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool},
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	}
	serverErrors := make(chan error, 4)
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
	if assistantServer != nil {
		go func() {
			serverErrors <- assistantServer.ListenAndServeTLS(configuration.AssistantMTLS.CertFile, configuration.AssistantMTLS.KeyFile)
		}()
	}
	if handoffServer != nil {
		go func() {
			serverErrors <- handoffServer.ListenAndServeTLS(configuration.AgentHandoff.CertFile, configuration.AgentHandoff.KeyFile)
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
	if assistantServer != nil {
		if err := assistantServer.Shutdown(shutdownContext); err != nil {
			logger.Error("assistant interface shutdown failed")
			_ = assistantServer.Close()
			os.Exit(1)
		}
	}
	if handoffServer != nil {
		if err := handoffServer.Shutdown(shutdownContext); err != nil {
			logger.Error("agent handoff interface shutdown failed")
			_ = handoffServer.Close()
			os.Exit(1)
		}
	}
	logger.Info("control service stopped")
}
