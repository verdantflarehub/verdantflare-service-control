package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment           string
	Address               string
	DatabaseURL           string
	DatabaseMaxOpen       int
	DatabaseMaxIdle       int
	GatewayBaseURL        string
	GatewayToken          string
	GatewayAdminToken     string
	LoginDirectoryBaseURL string
	LoginDirectoryToken   string
	DevLoginSubject       string
	TrustAuthHeaders      bool
	AllowedOrigins        []string
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	ShutdownTimeout       time.Duration
}

func Load() (Config, error) {
	environment := env("CONTROL_ENV", "development")
	trustHeaders, err := strconv.ParseBool(env("CONTROL_TRUST_AUTH_HEADERS", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("CONTROL_TRUST_AUTH_HEADERS: %w", err)
	}

	devSubject := env("CONTROL_DEV_LOGIN_SUBJECT", "00000000-0000-4000-8000-000000000001")
	if environment == "production" && devSubject != "" {
		return Config{}, fmt.Errorf("CONTROL_DEV_LOGIN_SUBJECT must be empty in production")
	}
	if environment == "production" && !trustHeaders {
		return Config{}, fmt.Errorf("CONTROL_TRUST_AUTH_HEADERS must be true in production until JWT validation is configured")
	}
	databaseURL := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_URL"))
	if environment == "production" && databaseURL == "" {
		return Config{}, fmt.Errorf("CONTROL_DATABASE_URL is required in production")
	}
	gatewayBaseURL := strings.TrimSpace(os.Getenv("CONTROL_GATEWAY_BASE_URL"))
	gatewayToken := strings.TrimSpace(os.Getenv("CONTROL_GATEWAY_TOKEN"))
	gatewayAdminToken := strings.TrimSpace(os.Getenv("CONTROL_GATEWAY_ADMIN_TOKEN"))
	loginDirectoryBaseURL := strings.TrimSpace(os.Getenv("CONTROL_LOGIN_DIRECTORY_BASE_URL"))
	loginDirectoryToken := strings.TrimSpace(os.Getenv("CONTROL_LOGIN_DIRECTORY_TOKEN"))
	if (loginDirectoryBaseURL == "") != (loginDirectoryToken == "") {
		return Config{}, fmt.Errorf("CONTROL_LOGIN_DIRECTORY_BASE_URL and CONTROL_LOGIN_DIRECTORY_TOKEN must be set together")
	}
	if loginDirectoryToken != "" && len(loginDirectoryToken) < 32 {
		return Config{}, fmt.Errorf("CONTROL_LOGIN_DIRECTORY_TOKEN must contain at least 32 characters")
	}
	if environment == "production" && strings.HasPrefix(strings.ToLower(gatewayBaseURL), "http://") {
		return Config{}, fmt.Errorf("CONTROL_GATEWAY_BASE_URL must use HTTPS in production")
	}
	if (gatewayBaseURL == "") != (gatewayToken == "") {
		return Config{}, fmt.Errorf("CONTROL_GATEWAY_BASE_URL and CONTROL_GATEWAY_TOKEN must be set together")
	}
	if gatewayAdminToken != "" && (gatewayBaseURL == "" || len(gatewayAdminToken) < 32) {
		return Config{}, fmt.Errorf("CONTROL_GATEWAY_ADMIN_TOKEN requires a gateway URL and at least 32 characters")
	}

	return Config{
		Environment:           environment,
		Address:               env("CONTROL_ADDRESS", ":8080"),
		DatabaseURL:           databaseURL,
		DatabaseMaxOpen:       positiveInt("CONTROL_DATABASE_MAX_OPEN", 5),
		DatabaseMaxIdle:       positiveInt("CONTROL_DATABASE_MAX_IDLE", 2),
		GatewayBaseURL:        gatewayBaseURL,
		GatewayToken:          gatewayToken,
		GatewayAdminToken:     gatewayAdminToken,
		LoginDirectoryBaseURL: loginDirectoryBaseURL,
		LoginDirectoryToken:   loginDirectoryToken,
		DevLoginSubject:       devSubject,
		TrustAuthHeaders:      trustHeaders,
		AllowedOrigins:        splitCSV(os.Getenv("CONTROL_ALLOWED_ORIGINS")),
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          15 * time.Second,
		ShutdownTimeout:       10 * time.Second,
	}, nil
}

func positiveInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
