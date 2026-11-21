package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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
	ChangeExchangeStatus(ctx context.Context, action string) (domain.ExchangeStatus, error)
	NewExchange(ctx context.Context) error
	PreviewDistribution(ctx context.Context) (bot.DistributionPreview, error)
	Distribute(ctx context.Context) (int, error)
	RemindNow(ctx context.Context) (int, error)
	Reassign(ctx context.Context, childID, giverID int64) error
	Broadcast(ctx context.Context, text string) (int, error)
	InviteLink() string
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
	mux.Handle("GET /api/state", s.auth(s.handleState))
	mux.Handle("POST /api/action", s.auth(s.handleAction))
	mux.Handle("POST /api/reassign", s.auth(s.handleReassign))
	mux.Handle("POST /api/broadcast", s.auth(s.handleBroadcast))
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

// ---------- state ----------

type personJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Phone    string `json:"phone,omitempty"`
	PVZ      string `json:"pvz,omitempty"`
}

type childJSON struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Gender string `json:"gender"`
	Wishes string `json:"wishes,omitempty"`
}

type familyJSON struct {
	personJSON
	Children []childJSON `json:"children"`
	Gives    int         `json:"gives"`
}

type assignmentJSON struct {
	ID           int64      `json:"id"`
	Status       string     `json:"status"`
	Child        childJSON  `json:"child"`
	Parent       personJSON `json:"parent"`
	Giver        personJSON `json:"giver"`
	ExpectedDate string     `json:"expected_date,omitempty"`
	OrderedAt    *time.Time `json:"ordered_at,omitempty"`
	ArrivedAt    *time.Time `json:"arrived_at,omitempty"`
	ReceivedAt   *time.Time `json:"received_at,omitempty"`
	CodeValid    bool       `json:"code_valid"`
	CodePending  bool       `json:"code_pending"`
	DaysInPVZ    int        `json:"days_in_pvz"`
	Stuck        bool       `json:"stuck"`
}

type stateJSON struct {
	Exchange struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		Title  string `json:"title"`
	} `json:"exchange"`
	Settings struct {
		Budget        string `json:"budget"`
		OrderDeadline string `json:"order_deadline"`
		DeliveryBy    string `json:"delivery_by"`
	} `json:"settings"`
	Invite      string                  `json:"invite"`
	Preview     bot.DistributionPreview `json:"preview"`
	Families    []familyJSON            `json:"families"`
	Unfinished  []personJSON            `json:"without_children"`
	Assignments []assignmentJSON        `json:"assignments"`
}

func person(u store.User) personJSON {
	return personJSON{ID: u.ID, Name: strings.TrimSpace(u.FirstName + " " + u.LastName), Username: u.TgUsername, Phone: u.Phone, PVZ: u.PVZAddress}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.buildState(ctx)
	if err != nil {
		slog.Error("admin panel state failed", "error", err)
		writeError(w, http.StatusInternalServerError, "Не удалось загрузить данные.")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) buildState(ctx context.Context) (stateJSON, error) {
	var out stateJSON
	ex, err := s.store.CurrentExchange(ctx)
	if err != nil {
		return out, err
	}
	out.Exchange.ID, out.Exchange.Status, out.Exchange.Title = ex.ID, string(ex.Status), bot.StatusTitle(ex.Status)
	out.Settings.Budget = budgetText(s.cfg)
	out.Settings.OrderDeadline = s.cfg.OrderDeadline
	out.Settings.DeliveryBy = s.cfg.DeliveryTarget
	out.Invite = s.admin.InviteLink()

	if out.Preview, err = s.admin.PreviewDistribution(ctx); err != nil {
		return out, err
	}

	users, err := s.store.Users(ctx)
	if err != nil {
		return out, err
	}
	children, err := s.store.ExchangeChildren(ctx, ex.ID)
	if err != nil {
		return out, err
	}
	assignments, err := s.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return out, err
	}

	kids := map[int64][]childJSON{}
	for _, c := range children {
		kids[c.ParentID] = append(kids[c.ParentID], childJSON{ID: c.ID, Name: c.FirstName + " " + c.LastName, Age: c.Age, Gender: c.Gender, Wishes: c.Wishes})
	}
	gives := map[int64]int{}
	for _, a := range assignments {
		gives[a.Giver.ID]++
	}
	out.Families = []familyJSON{}
	out.Unfinished = []personJSON{}
	for _, u := range users {
		if len(kids[u.ID]) == 0 {
			out.Unfinished = append(out.Unfinished, person(u))
			continue
		}
		out.Families = append(out.Families, familyJSON{personJSON: person(u), Children: kids[u.ID], Gives: gives[u.ID]})
	}

	now := s.now()
	out.Assignments = []assignmentJSON{}
	for _, a := range assignments {
		ps := a.PickupState()
		item := assignmentJSON{
			ID:          a.ID,
			Status:      string(a.Status),
			Child:       childJSON{ID: a.ChildID, Name: a.ChildFirstName + " " + a.ChildLastName, Age: a.ChildAge, Gender: a.ChildGender, Wishes: a.ChildWishes},
			Parent:      person(a.Parent),
			Giver:       person(a.Giver),
			OrderedAt:   a.OrderedAt,
			ArrivedAt:   a.ArrivedAt,
			ReceivedAt:  a.ReceivedAt,
			CodeValid:   a.Status == domain.AssignmentArrived && ps.CodeValid(now),
			CodePending: a.Status == domain.AssignmentArrived && ps.CodeRequestPending(),
		}
		if a.ExpectedDate != nil {
			item.ExpectedDate = a.ExpectedDate.Format("2006-01-02")
		}
		if a.Status == domain.AssignmentArrived && a.ArrivedAt != nil {
			waited := now.Sub(*a.ArrivedAt)
			item.DaysInPVZ = int(waited.Hours() / 24)
			item.Stuck = waited >= s.cfg.StuckAfter
		}
		out.Assignments = append(out.Assignments, item)
	}
	return out, nil
}

func budgetText(cfg config.Config) string {
	return fmt.Sprintf("до %d %s", cfg.GiftBudgetMax, cfg.GiftCurrency)
}

// ---------- actions ----------

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
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
		if to, err = s.admin.ChangeExchangeStatus(ctx, req.Action); err == nil {
			msg = "Готово: " + bot.StatusTitle(to) + "."
		}
	case "new_exchange":
		if err = s.admin.NewExchange(ctx); err == nil {
			msg = "Создан новый обмен."
		}
	case "distribute":
		var n int
		if n, err = s.admin.Distribute(ctx); err == nil {
			msg = "Распределение готово: " + strconv.Itoa(n) + " подарков. Участникам отправлены карточки."
		}
	case "remind":
		var n int
		if n, err = s.admin.RemindNow(ctx); err == nil {
			msg = "Отправлено напоминаний: " + strconv.Itoa(n) + "."
		}
	default:
		writeError(w, http.StatusBadRequest, "Неизвестное действие.")
		return
	}
	s.reply(w, msg, err)
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
