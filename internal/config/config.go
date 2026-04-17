package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	App   AppConfig
	API   APIConfig
	DB    DBConfig
	SFTP  SFTPConfig
	Email EmailConfig
	Retry RetryConfig
}

type AppConfig struct {
	LoopInterval           time.Duration
	WorkerCount            int
	DataDir                string
	OverlapDays            int
	RetentionSentDays      int
	RetentionCleanupEvery  time.Duration
	RetryWorkerEvery       time.Duration
	RetryBatchSize         int
	RetryWorkerConcurrency int
}

type APIConfig struct {
	BaseURL       string
	AuthPath      string
	ListPath      string
	SignedURLPath string
	BodyUsername  string
	BodyPassword  string
	TipoLogin     string
	BasicUsername string
	BasicPassword string
	Timeout       time.Duration
	TokenSkew     time.Duration
}

type DBConfig struct {
	DSN string
}

type SFTPConfig struct {
	Host      string
	Port      int
	User      string
	Password  string
	RemoteDir string
	Timeout   time.Duration
}

type EmailConfig struct {
	Enabled  bool
	Host     string
	Port     int
	User     string
	Password string
	From     string
	To       string
}

type RetryConfig struct {
	MaxAttempts int
	JitterPct   int
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		App: AppConfig{
			LoopInterval:           time.Duration(getInt("APP_LOOP_MINUTES", 2)) * time.Minute,
			WorkerCount:            getInt("APP_WORKERS", 5),
			DataDir:                getString("APP_DATA_DIR", "/data"),
			OverlapDays:            getInt("APP_OVERLAP_DAYS", 1),
			RetentionSentDays:      getInt("RETENTION_SENT_DAYS", 2),
			RetentionCleanupEvery:  time.Duration(getInt("RETENTION_CLEANUP_MINUTES", 60)) * time.Minute,
			RetryWorkerEvery:       time.Duration(getInt("RETRY_WORKER_SECONDS", 30)) * time.Second,
			RetryBatchSize:         getInt("RETRY_BATCH_SIZE", 50),
			RetryWorkerConcurrency: getInt("RETRY_WORKER_CONCURRENCY", 3),
		},
		API: APIConfig{
			BaseURL:       getString("API_BASE_URL", "https://api.medlogperu.com.pe"),
			AuthPath:      getString("API_AUTH_PATH", "/v2/auth/login"),
			ListPath:      getString("API_LIST_PATH", "/v2/external/file/listado-raw"),
			SignedURLPath: getString("API_SIGNED_URL_PATH", "/v2/resources/files/signed-url"),
			BodyUsername:  getString("API_BODY_USERNAME", "PDP.PBI"),
			BodyPassword:  os.Getenv("API_BODY_PASSWORD"),
			TipoLogin:     getString("API_TIPO_LOGIN", "A"),
			BasicUsername: getString("API_BASIC_USERNAME", "PDPEDI"),
			BasicPassword: os.Getenv("API_BASIC_PASSWORD"),
			Timeout:       time.Duration(getInt("API_TIMEOUT_SECONDS", 45)) * time.Second,
			TokenSkew:     time.Duration(getInt("TOKEN_SKEW_SECONDS", 60)) * time.Second,
		},
		DB: DBConfig{
			DSN: getString("DB_DSN", "postgres://postgres:postgres@localhost:5432/extract_coparn?sslmode=disable"),
		},
		SFTP: SFTPConfig{
			Host:      getString("SFTP_HOST", "10.20.12.30"),
			Port:      getInt("SFTP_PORT", 22),
			User:      getString("SFTP_USER", "testsftp"),
			Password:  os.Getenv("SFTP_PASSWORD"),
			RemoteDir: getString("SFTP_REMOTE_DIR", "/var/ftp/test-sftp/MSC/inbound"),
			Timeout:   time.Duration(getInt("SFTP_TIMEOUT_SECONDS", 30)) * time.Second,
		},
		Email: EmailConfig{
			Enabled:  getBool("ALERT_EMAIL_ENABLED", false),
			Host:     getString("ALERT_SMTP_HOST", ""),
			Port:     getInt("ALERT_SMTP_PORT", 587),
			User:     getString("ALERT_SMTP_USER", ""),
			Password: os.Getenv("ALERT_SMTP_PASSWORD"),
			From:     getString("ALERT_EMAIL_FROM", ""),
			To:       getString("ALERT_EMAIL_TO", "stornblood6969@gmail.com"),
		},
		Retry: RetryConfig{
			MaxAttempts: getInt("RETRY_MAX_ATTEMPTS", 10),
			JitterPct:   getInt("RETRY_JITTER_PCT", 20),
		},
	}

	if cfg.API.BodyPassword == "" || cfg.API.BasicPassword == "" {
		return nil, fmt.Errorf("faltan credenciales del API en entorno")
	}
	if cfg.SFTP.Password == "" {
		return nil, fmt.Errorf("falta SFTP_PASSWORD en entorno")
	}
	if cfg.App.WorkerCount <= 0 {
		cfg.App.WorkerCount = 5
	}
	if cfg.App.LoopInterval < time.Minute {
		cfg.App.LoopInterval = time.Minute
	}
	return cfg, nil
}

func getString(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
