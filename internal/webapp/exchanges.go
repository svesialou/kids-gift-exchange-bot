package webapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/bot"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

// Dates go to the browser as local wall-clock strings in the bot timezone, so admins see the same
// times whatever timezone their phone is set to.
const localDateTime = "2006-01-02T15:04"

type personJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Phone    string `json:"phone,omitempty"`
	PVZ      string `json:"pvz,omitempty"`
	Banned   bool   `json:"banned,omitempty"`
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
	Children   []childJSON `json:"children"`
	Gives      int         `json:"gives"`
	Violations int         `json:"violations"`
}

type assignmentJSON struct {
	ID           int64      `json:"id"`
	ExchangeID   int64      `json:"exchange_id"`
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

type violationJSON struct {
	ID            int64     `json:"id"`
	ExchangeID    int64     `json:"exchange_id"`
	ExchangeTitle string    `json:"exchange_title"`
	UserID        int64     `json:"user_id"`
	UserName      string    `json:"user_name"`
	ChildName     string    `json:"child_name,omitempty"`
	Kind          string    `json:"kind"`
	Comment       string    `json:"comment,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Dismissed     bool      `json:"dismissed"`
}

type settingsJSON struct {
	Title                string `json:"title"`
	BudgetMin            int    `json:"budget_min"`
	RegistrationOpensAt  string `json:"registration_opens_at"`
	RegistrationClosesAt string `json:"registration_closes_at"`
	OrderDeadline        string `json:"order_deadline"`
	DeliveryBy           string `json:"delivery_by"`
}

type exchangeJSON struct {
	ID            int64        `json:"id"`
	Status        string       `json:"status"`
	StatusTitle   string       `json:"status_title"`
	Active        bool         `json:"active"`
	CreatedAt     time.Time    `json:"created_at"`
	DistributedAt *time.Time   `json:"distributed_at,omitempty"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
	Settings      settingsJSON `json:"settings"`
}

type exchangeSummaryJSON struct {
	exchangeJSON
	Families   int `json:"families"`
	Children   int `json:"children"`
	Gifts      int `json:"gifts"`
	Received   int `json:"received"`
	Violations int `json:"violations"`
}

type exchangeDetailJSON struct {
	Exchange    exchangeJSON     `json:"exchange"`
	Families    []familyJSON     `json:"families"`
	Assignments []assignmentJSON `json:"assignments"`
	Violations  []violationJSON  `json:"violations"`

	Invite     string                  `json:"invite"`
	Preview    bot.DistributionPreview `json:"preview"`
	Unfinished []personJSON            `json:"without_children"`
	// CanOpen tells whether registration may be opened now; OpenHint explains why not.
	CanOpen  bool   `json:"can_open"`
	OpenHint string `json:"open_hint,omitempty"`
}

func person(u store.User) personJSON {
	return personJSON{ID: u.ID, Name: strings.TrimSpace(u.FirstName + " " + u.LastName), Username: u.TgUsername,
		Phone: u.Phone, PVZ: u.PVZAddress, Banned: u.BannedAt != nil}
}

func (s *Server) settingsToJSON(st bot.Settings) settingsJSON {
	out := settingsJSON{Title: st.Title, BudgetMin: st.BudgetMin}
	if st.RegistrationOpensAt != nil {
		out.RegistrationOpensAt = st.RegistrationOpensAt.In(s.cfg.Location).Format(localDateTime)
	}
	if st.RegistrationClosesAt != nil {
		out.RegistrationClosesAt = st.RegistrationClosesAt.In(s.cfg.Location).Format(localDateTime)
	}
	if !st.OrderDeadline.IsZero() {
		out.OrderDeadline = st.OrderDeadline.Format(time.DateOnly)
	}
	if !st.DeliveryBy.IsZero() {
		out.DeliveryBy = st.DeliveryBy.Format(time.DateOnly)
	}
	return out
}

func (s *Server) settingsFromJSON(in settingsJSON) (bot.Settings, error) {
	out := bot.Settings{Title: in.Title, BudgetMin: in.BudgetMin}
	var err error
	parseLocal := func(v string) *time.Time {
		if v == "" || err != nil {
			return nil
		}
		t, e := time.ParseInLocation(localDateTime, v, s.cfg.Location)
		if e != nil {
			err = e
			return nil
		}
		return &t
	}
	out.RegistrationOpensAt = parseLocal(in.RegistrationOpensAt)
	out.RegistrationClosesAt = parseLocal(in.RegistrationClosesAt)
	if in.OrderDeadline != "" {
		if out.OrderDeadline, err = time.Parse(time.DateOnly, in.OrderDeadline); err != nil {
			return out, err
		}
	}
	if in.DeliveryBy != "" {
		if out.DeliveryBy, err = time.Parse(time.DateOnly, in.DeliveryBy); err != nil {
			return out, err
		}
	}
	return out, err
}

func (s *Server) exchangeToJSON(ex store.Exchange) exchangeJSON {
	return exchangeJSON{
		ID: ex.ID, Status: string(ex.Status), StatusTitle: bot.StatusTitle(ex.Status),
		Active:    ex.Status == domain.ExchangeOpen || ex.Status == domain.ExchangeClosed || ex.Status == domain.ExchangeDistributed,
		CreatedAt: ex.CreatedAt, DistributedAt: ex.DistributedAt, CompletedAt: ex.CompletedAt,
		Settings: s.settingsToJSON(s.admin.EffectiveSettings(ex)),
	}
}

func violationToJSON(v store.Violation) violationJSON {
	return violationJSON{
		ID: v.ID, ExchangeID: v.ExchangeID, ExchangeTitle: v.ExchangeTitle, UserID: v.UserID, UserName: v.UserName,
		ChildName: v.ChildName, Kind: string(v.Kind), Comment: v.Comment, CreatedAt: v.CreatedAt, Dismissed: v.DismissedAt != nil,
	}
}

func (s *Server) assignmentToJSON(a store.Assignment, now time.Time) assignmentJSON {
	ps := a.PickupState()
	item := assignmentJSON{
		ID:          a.ID,
		ExchangeID:  a.ExchangeID,
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
		item.ExpectedDate = a.ExpectedDate.Format(time.DateOnly)
	}
	if a.Status == domain.AssignmentArrived && a.ArrivedAt != nil {
		waited := now.Sub(*a.ArrivedAt)
		item.DaysInPVZ = int(waited.Hours() / 24)
		item.Stuck = waited >= s.cfg.StuckAfter
	}
	return item
}

// ---------- handlers ----------

func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ex, err := s.store.ExchangeByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Обмен не найден.")
		return
	}
	if err != nil {
		s.fail(w, "exchange", err)
		return
	}
	out, err := s.buildExchange(ctx, ex)
	if err != nil {
		s.fail(w, "exchange", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) buildExchange(ctx context.Context, ex store.Exchange) (exchangeDetailJSON, error) {
	out := exchangeDetailJSON{Exchange: s.exchangeToJSON(ex)}
	open := ex.Status == domain.ExchangeOpen

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
	violations, err := s.store.ViolationsByExchange(ctx, ex.ID)
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
	violationsOf := map[int64]int{}
	out.Violations = []violationJSON{}
	for _, v := range violations {
		if v.DismissedAt == nil {
			violationsOf[v.UserID]++
		}
		out.Violations = append(out.Violations, violationToJSON(v))
	}

	out.Families = []familyJSON{}
	var unfinished []personJSON
	for _, u := range users {
		if len(kids[u.ID]) > 0 || gives[u.ID] > 0 {
			children := kids[u.ID]
			if children == nil {
				children = []childJSON{}
			}
			out.Families = append(out.Families, familyJSON{personJSON: person(u), Children: children, Gives: gives[u.ID], Violations: violationsOf[u.ID]})
		} else if open {
			unfinished = append(unfinished, person(u))
		}
	}

	now := s.now()
	out.Assignments = []assignmentJSON{}
	for _, a := range assignments {
		out.Assignments = append(out.Assignments, s.assignmentToJSON(a, now))
	}

	out.Invite = s.admin.InviteLink()
	if out.Preview, err = s.admin.PreviewDistribution(ctx, ex.ID); err != nil {
		return out, err
	}
	out.Unfinished = unfinished
	if out.Unfinished == nil {
		out.Unfinished = []personJSON{}
	}
	if ex.Status == domain.ExchangeDraft || ex.Status == domain.ExchangeClosed {
		out.CanOpen, out.OpenHint = s.canOpen(ctx, ex)
	}
	return out, nil
}

func (s *Server) handleExchanges(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListExchanges(r.Context())
	if err != nil {
		s.fail(w, "exchanges", err)
		return
	}
	out := make([]exchangeSummaryJSON, 0, len(list))
	for _, x := range list {
		out = append(out, exchangeSummaryJSON{
			exchangeJSON: s.exchangeToJSON(x.Exchange),
			Families:     x.Families, Children: x.Children, Gifts: x.Gifts, Received: x.Received, Violations: x.Violations,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	settings, ok := s.readSettings(w, r)
	if !ok {
		return
	}
	err := s.admin.UpdateSettings(r.Context(), id, settings)
	s.reply(w, "Сохранено. Новые сообщения бота будут с этими условиями, уже отправленные не изменятся.", err)
}

func (s *Server) handleCreateExchange(w http.ResponseWriter, r *http.Request) {
	settings, ok := s.readSettings(w, r)
	if !ok {
		return
	}
	id, err := s.admin.NewExchange(r.Context(), settings)
	if err != nil {
		s.reply(w, "", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Обмен создан. Регистрацию можно открыть в его даты.", "id": id})
}

func (s *Server) readSettings(w http.ResponseWriter, r *http.Request) (bot.Settings, bool) {
	var req settingsJSON
	if !readJSON(w, r, &req) {
		return bot.Settings{}, false
	}
	settings, err := s.settingsFromJSON(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Некорректная дата.")
		return bot.Settings{}, false
	}
	return settings, true
}

func (s *Server) fail(w http.ResponseWriter, op string, err error) {
	slog.Error("admin panel request failed", "op", op, "error", err)
	writeError(w, http.StatusInternalServerError, "Не удалось загрузить данные.")
}

func (s *Server) handleDeleteExchange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Notify bool `json:"notify"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.reply(w, "Обмен удалён.", s.admin.DeleteExchange(r.Context(), id, req.Notify))
}

// canOpen mirrors the bot's checks so the panel can grey out the button with a reason.
func (s *Server) canOpen(ctx context.Context, ex store.Exchange) (bool, string) {
	if ex.RegistrationOpensAt == nil || ex.RegistrationClosesAt == nil {
		return false, "Укажите даты регистрации в условиях."
	}
	now := s.now()
	if now.Before(*ex.RegistrationOpensAt) {
		return false, "Регистрацию можно открыть с " + ex.RegistrationOpensAt.In(s.cfg.Location).Format("02.01 15:04") + "."
	}
	if !now.Before(*ex.RegistrationClosesAt) {
		return false, "Время регистрации прошло. Чтобы открыть, перенесите её конец в условиях."
	}
	active, err := s.store.ActiveExchange(ctx)
	if err == nil && active.ID != 0 && active.ID != ex.ID {
		return false, "Сейчас идёт «" + s.admin.EffectiveSettings(active).Title + "». Одновременно может идти только один обмен."
	}
	return true, ""
}
