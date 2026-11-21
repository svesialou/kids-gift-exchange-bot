package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/bot"
	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

//go:embed static
var staticFS embed.FS

const initDataMaxAge = 24 * time.Hour

// Admin is the part of the bot the panel drives; every action also notifies participants in Telegram.
type Admin interface {
	ChangeExchangeStatus(ctx context.Context, id int64, action string) (domain.ExchangeStatus, error)
	NewExchange(ctx context.Context, s bot.Settings) (int64, error)
	PreviewDistribution(ctx context.Context, id int64) (bot.DistributionPreview, error)
	Distribute(ctx context.Context, id int64) (int, error)
	RemindNow(ctx context.Context) (int, error)
	Reassign(ctx context.Context, childID, giverID int64) error
	Broadcast(ctx context.Context, text string) (int, error)
	InviteLink() string
	EffectiveSettings(ex store.Exchange) bot.Settings
	UpdateSettings(ctx context.Context, id int64, s bot.Settings) error
	SetBanned(ctx context.Context, userIDs []int64, banned bool, reason string) (int, error)
	SendMessage(ctx context.Context, userIDs []int64, text string) (int, error)
	DeleteExchange(ctx context.Context, id int64, notify bool) error
	AddViolation(ctx context.Context, exchangeID, userID int64, comment string) error
	SetViolationDismissed(ctx context.Context, id int64, dismissed bool) error
}

type Server struct {
	cfg   config.Config
	store *store.Store
	admin Admin
	now   func() time.Time
}

func New(cfg config.Config, st *store.Store, admin Admin) *Server {
	return &Server{cfg: cfg, store: st, admin: admin, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("GET /api/exchanges", s.auth(s.handleExchanges))
	mux.Handle("GET /api/exchanges/{id}", s.auth(s.handleExchange))
	mux.Handle("POST /api/exchanges", s.auth(s.handleCreateExchange))
	mux.Handle("POST /api/exchanges/{id}/settings", s.auth(s.handleSettings))
	mux.Handle("POST /api/exchanges/{id}/action", s.auth(s.handleAction))
	mux.Handle("POST /api/remind", s.auth(s.handleRemind))
	mux.Handle("POST /api/reassign", s.auth(s.handleReassign))
	mux.Handle("POST /api/broadcast", s.auth(s.handleBroadcast))
	mux.Handle("GET /api/users", s.auth(s.handleUsers))
	mux.Handle("GET /api/users/{id}", s.auth(s.handleUser))
	mux.Handle("POST /api/users/ban", s.auth(s.handleBan))
	mux.Handle("POST /api/messages", s.auth(s.handleMessage))
	mux.Handle("POST /api/exchanges/{id}/delete", s.auth(s.handleDeleteExchange))
	mux.Handle("POST /api/violations", s.auth(s.handleAddViolation))
	mux.Handle("POST /api/violations/{id}/dismiss", s.auth(s.handleDismissViolation))
	return securityHeaders(mux)
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Distribution sends many Telegram messages inside one request.
		WriteTimeout: 5 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	slog.Info("admin panel listening", "addr", s.cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://telegram.org; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self' https://web.telegram.org https://*.telegram.org")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// auth accepts only requests signed by Telegram for a user listed in ADMIN_IDS.
func (s *Server) auth(next func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "tma ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "Откройте панель из Telegram.")
			return
		}
		userID, err := validateInitData(raw, s.cfg.TelegramToken, initDataMaxAge, s.now())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Сессия устарела, откройте панель заново.")
			return
		}
		if !s.cfg.IsAdmin(userID) {
			slog.Warn("admin panel access denied", "tg_id", userID)
			writeError(w, http.StatusForbidden, "Панель доступна только организаторам.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		next(w, r)
	})
}

// ---------- actions ----------

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	var msg string
	var err error
	switch req.Action {
	case "open", "close", "finish":
		var to domain.ExchangeStatus
		if to, err = s.admin.ChangeExchangeStatus(ctx, id, req.Action); err == nil {
			msg = "Готово: " + bot.StatusTitle(to) + "."
		}
	case "distribute":
		var n int
		if n, err = s.admin.Distribute(ctx, id); err == nil {
			msg = "Распределение готово: " + strconv.Itoa(n) + " подарков. Участникам отправлены карточки."
		}
	default:
		writeError(w, http.StatusBadRequest, "Неизвестное действие.")
		return
	}
	s.reply(w, msg, err)
}

func (s *Server) handleRemind(w http.ResponseWriter, r *http.Request) {
	n, err := s.admin.RemindNow(r.Context())
	s.reply(w, "Отправлено напоминаний: "+strconv.Itoa(n)+".", err)
}

func (s *Server) handleReassign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChildID int64 `json:"child_id"`
		GiverID int64 `json:"giver_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	err := s.admin.Reassign(r.Context(), req.ChildID, req.GiverID)
	s.reply(w, "Подарок передан, новый даритель получил карточку.", err)
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.admin.Broadcast(r.Context(), req.Text)
	s.reply(w, "Отправлено "+strconv.Itoa(n)+" участникам.", err)
}

func (s *Server) reply(w http.ResponseWriter, msg string, err error) {
	var ae *bot.ActionError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
	case errors.As(err, &ae):
		writeError(w, http.StatusConflict, ae.Msg)
	default:
		slog.Error("admin panel action failed", "error", err)
		writeError(w, http.StatusInternalServerError, "Действие не выполнено, подробности в логах бота.")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "Некорректный запрос.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Некорректный id.")
		return 0, false
	}
	return id, true
}
