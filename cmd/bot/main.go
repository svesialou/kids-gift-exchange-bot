package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	_ "github.com/go-sql-driver/mysql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/bot"
	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/webapp"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("bot stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	st, err := store.Open(openCtx, cfg.DB.DSN())
	cancel()
	if err != nil {
		return err
	}
	defer st.Close()

	version, err := st.Migrate()
	if err != nil {
		return err
	}
	slog.Info("migrations applied", "version", version)

	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return err
	}
	slog.Info("bot started", "username", api.Self.UserName, "admins", len(cfg.AdminIDs))

	b := bot.New(api, st, cfg)
	if err := b.LoadSettings(ctx); err != nil {
		return err
	}
	if err := b.Texts().Load(ctx); err != nil {
		return err
	}
	go b.RunScheduler(ctx)
	if cfg.WebAppURL != "" {
		b.SetupAdminMenuButtons()
		go func() {
			if err := webapp.New(cfg, st, b).Run(ctx); err != nil {
				slog.Error("admin panel stopped", "error", err)
				stop()
			}
		}()
	}
	return b.Run(ctx)
}
