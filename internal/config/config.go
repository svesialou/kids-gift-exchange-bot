package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TelegramToken string
	AdminIDs      []int64

	// WebAppURL is the public HTTPS address of the admin panel; empty disables the HTTP server.
	WebAppURL string
	HTTPAddr  string

	DB DB

	Location *time.Location

	MorningHour      int
	EveningHour      int
	QuietHour        int
	CodePendingAfter time.Duration
	StuckAfter       time.Duration
	SchedulerEvery   time.Duration
}

type DB struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
}

func (d DB) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=UTC&multiStatements=true&charset=utf8mb4",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

func Load() (Config, error) {
	var errs []string
	str := func(key, def string) string {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	num := func(key string, def int) int {
		raw := str(key, "")
		if raw == "" {
			return def
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: not an integer", key))
		}
		return v
	}
	dur := func(key string, def time.Duration) time.Duration {
		raw := str(key, "")
		if raw == "" {
			return def
		}
		v, err := time.ParseDuration(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: invalid duration", key))
		}
		return v
	}

	cfg := Config{
		TelegramToken: str("TELEGRAM_BOT_TOKEN", ""),
		WebAppURL:     strings.TrimRight(str("WEBAPP_URL", ""), "/"),
		HTTPAddr:      str("HTTP_ADDR", "127.0.0.1:18090"),
		DB: DB{
			Host:     str("DB_HOST", "127.0.0.1"),
			Port:     num("DB_PORT", 3306),
			Name:     str("DB_NAME", "kids_gift_exchange"),
			User:     str("DB_USER", "kids_gift_exchange"),
			Password: str("DB_PASSWORD", ""),
		},
		MorningHour:      num("MORNING_HOUR", 10),
		EveningHour:      num("EVENING_HOUR", 19),
		QuietHour:        num("QUIET_HOUR", 22),
		CodePendingAfter: dur("CODE_PENDING_REMINDER_AFTER", 2*time.Hour),
		StuckAfter:       dur("STUCK_AFTER", 96*time.Hour),
		SchedulerEvery:   dur("SCHEDULER_INTERVAL", 10*time.Minute),
	}

	if cfg.WebAppURL != "" && !strings.HasPrefix(cfg.WebAppURL, "https://") {
		errs = append(errs, "WEBAPP_URL must start with https://")
	}
	if cfg.TelegramToken == "" {
		errs = append(errs, "TELEGRAM_BOT_TOKEN is required")
	}

	for _, part := range strings.Split(str("ADMIN_IDS", ""), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Sprintf("ADMIN_IDS: %q is not a Telegram user id", part))
			continue
		}
		cfg.AdminIDs = append(cfg.AdminIDs, id)
	}

	loc, err := time.LoadLocation(str("TIMEZONE", "Europe/Minsk"))
	if err != nil {
		errs = append(errs, "TIMEZONE: unknown IANA zone")
	}
	cfg.Location = loc

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid config: %s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

func (c Config) IsAdmin(tgID int64) bool {
	for _, id := range c.AdminIDs {
		if id == tgID {
			return true
		}
	}
	return false
}
