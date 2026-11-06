package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Redis       RedisConfig
	Auth        AuthConfig
	Admin       AdminConfig
	SMTP        SMTPConfig
	FileStorage FileStorageConfig
	Portal      PortalConfig
	App         AppConfig
}

type ServerConfig struct {
	Port           int
	Mode           string   // debug, release
	AllowedOrigins []string // CORS allowed origins; empty = allow all (*)
}

type DatabaseConfig struct {
	Host        string
	Port        int
	Name        string
	User        string
	Password    string
	MaxConns    int
	SSLMode     string // disable, prefer, require, verify-ca, verify-full
	SSLRootCert string // path to CA certificate for verify-ca/verify-full
}

type RedisConfig struct {
	Host          string
	Port          int
	Password      string
	DB            int
	TLSEnabled    bool
	TLSSkipVerify bool
}

type AuthConfig struct {
	JWTSecret     string
	JWTExpires    time.Duration
	AdminMode     string // mvp, production
	EncryptionKey string // AES-256 key for encrypting upstream API keys (32+ bytes)
}

type AdminConfig struct {
	MaxSystemAdmins int
	MaxSuperAdmins  int
	MaxAdminsMVP    int
}

type SMTPConfig struct {
	Host     string
	Port     int
	Secure   bool // Use SSL/TLS
	User     string
	Password string
	From     string
}

type FileStorageConfig struct {
	Provider         string
	LocalRoot        string
	Endpoint         string
	Region           string
	Bucket           string
	AccessKey        string
	SecretKey        string
	UseSSL           bool
	PathPrefix       string
	MaxSizeMB        int64
	MaxTotalMBPerApp int64
	AllowedTypes     []string
}

type PortalConfig struct {
	SyncSecret string
}

type AppConfig struct {
	BaseURL string // Base URL for password reset links, etc.
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Port:           getEnvInt("PORT", 3001),
			Mode:           getEnv("GIN_MODE", "debug"),
			AllowedOrigins: getEnvStringSlice("ALLOWED_ORIGINS"),
		},
		Database: DatabaseConfig{
			Host:        getEnv("DB_HOST", "127.0.0.1"),
			Port:        getEnvInt("DB_PORT", 5432),
			Name:        getEnv("DB_NAME", "ai_proxy_portal"),
			User:        getEnv("DB_USER", "postgres"),
			Password:    getEnv("DB_PASSWORD", ""),
			MaxConns:    getEnvInt("DB_POOL_MAX", 20),
			SSLMode:     getEnv("DB_SSLMODE", "disable"),
			SSLRootCert: getEnv("DB_SSL_ROOT_CERT", ""),
		},
		Redis: RedisConfig{
			Host:          getEnv("REDIS_HOST", "127.0.0.1"),
			Port:          getEnvInt("REDIS_PORT", 6379),
			Password:      getEnv("REDIS_PASSWORD", ""),
			DB:            getEnvInt("REDIS_DB", 0),
			TLSEnabled:    getEnvBool("REDIS_TLS", false),
			TLSSkipVerify: getEnvBool("REDIS_TLS_SKIP_VERIFY", false),
		},
		Auth: AuthConfig{
			JWTSecret:     getEnv("JWT_SECRET", ""),
			JWTExpires:    getEnvDuration("JWT_EXPIRES", 7*24*time.Hour),
			AdminMode:     getEnv("ADMIN_MODE", "production"),
			EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		},
		Admin: AdminConfig{
			MaxSystemAdmins: getEnvInt("MAX_SYSTEM_ADMINS", 10),
			MaxSuperAdmins:  getEnvInt("MAX_SUPER_ADMINS", 3),
			MaxAdminsMVP:    getEnvInt("MAX_ADMINS", 5),
		},
		SMTP: SMTPConfig{
			Host:     getEnv("SMTP_HOST", ""),
			Port:     getEnvInt("SMTP_PORT", 465),
			Secure:   getEnvBool("SMTP_SECURE", true),
			User:     getEnv("SMTP_USER", ""),
			Password: getEnv("SMTP_PASSWORD", ""),
			From:     getEnv("SMTP_FROM", ""),
		},
		FileStorage: FileStorageConfig{
			Provider:         getEnv("FILE_STORAGE_PROVIDER", "local"),
			LocalRoot:        getEnv("FILE_STORAGE_LOCAL_ROOT", "./uploads"),
			Endpoint:         getEnv("FILE_STORAGE_ENDPOINT", ""),
			Region:           getEnv("FILE_STORAGE_REGION", "ap-southeast-1"),
			Bucket:           getEnv("FILE_STORAGE_BUCKET", "ai-portal-uploads"),
			AccessKey:        getEnv("FILE_STORAGE_ACCESS_KEY", ""),
			SecretKey:        getEnv("FILE_STORAGE_SECRET_KEY", ""),
			UseSSL:           getEnvBool("FILE_STORAGE_USE_SSL", true),
			PathPrefix:       getEnv("FILE_STORAGE_PATH_PREFIX", "enterprise-apps"),
			MaxSizeMB:        getEnvInt64("FILE_MAX_SIZE_MB", 10),
			MaxTotalMBPerApp: getEnvInt64("FILE_MAX_TOTAL_MB_PER_APPLICATION", 50),
			AllowedTypes:     getEnvStringSlice("FILE_ALLOWED_TYPES"),
		},
		Portal: PortalConfig{
			SyncSecret: getEnv("PROXY_SYNC_SECRET", ""),
		},
		App: AppConfig{
			BaseURL: getEnv("APP_BASE_URL", "https://portal.example.com"),
		},
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getEnvStringSlice(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
