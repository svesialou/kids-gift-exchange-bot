package webapp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

type userRowJSON struct {
	personJSON
	CreatedAt  time.Time `json:"created_at"`
	BanReason  string    `json:"ban_reason,omitempty"`
	Exchanges  int       `json:"exchanges"`
	GiftsGiven int       `json:"gifts_given"`
	Violations int       `json:"violations"`
	RedFlag    bool      `json:"red_flag"`
}

type userExchangeJSON struct {
	ExchangeID int64            `json:"exchange_id"`
	Title      string           `json:"title"`
	Status     string           `json:"status_title"`
	Children   []childJSON      `json:"children"`
	Gave       []assignmentJSON `json:"gave"`
	Received   []assignmentJSON `json:"received"`
}

type userDetailJSON struct {
	User       userRowJSON        `json:"user"`
	Exchanges  []userExchangeJSON `json:"exchanges"`
	Violations []violationJSON    `json:"violations"`
}

func userRow(st store.UserStat) userRowJSON {
	return userRowJSON{
		personJSON: person(st.User), CreatedAt: st.CreatedAt, BanReason: st.BanReason,
		Exchanges: st.Exchanges, GiftsGiven: st.GiftsGiven, Violations: st.Violations,
		RedFlag: st.Violations > 0 || st.BannedAt != nil,
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.UserStats(r.Context())
	if err != nil {
		s.fail(w, "users", err)
		return
	}
	out := make([]userRowJSON, 0, len(stats))
	for _, st := range stats {
		out = append(out, userRow(st))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	stats, err := s.store.UserStats(ctx)
	if err != nil {
		s.fail(w, "user", err)
		return
	}
	var row *store.UserStat
	for i := range stats {
		if stats[i].ID == id {
			row = &stats[i]
			break
		}
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "Пользователь не найден.")
		return
	}

	exchanges, err := s.store.ListExchanges(ctx)
	if err != nil {
		s.fail(w, "user", err)
		return
	}
	children, err := s.store.ChildrenOfUser(ctx, id)
	if err != nil {
		s.fail(w, "user", err)
		return
	}
	assignments, err := s.store.AssignmentsOfUser(ctx, id)
	if err != nil {
		s.fail(w, "user", err)
		return
	}
	violations, err := s.store.ViolationsByUser(ctx, id)
	if err != nil {
		s.fail(w, "user", err)
		return
	}

	byExchange := map[int64]*userExchangeJSON{}
	entry := func(exchangeID int64) *userExchangeJSON {
		if e, ok := byExchange[exchangeID]; ok {
			return e
		}
		e := &userExchangeJSON{ExchangeID: exchangeID, Children: []childJSON{}, Gave: []assignmentJSON{}, Received: []assignmentJSON{}}
		byExchange[exchangeID] = e
		return e
	}
	for _, c := range children {
		e := entry(c.ExchangeID)
		e.Children = append(e.Children, childJSON{ID: c.ID, Name: c.FirstName + " " + c.LastName, Age: c.Age, Gender: c.Gender, Wishes: c.Wishes})
	}
	now := s.now()
	for _, a := range assignments {
		e := entry(a.ExchangeID)
		if a.Giver.ID == id {
			e.Gave = append(e.Gave, s.assignmentToJSON(a, now))
		}
		if a.Parent.ID == id {
			e.Received = append(e.Received, s.assignmentToJSON(a, now))
		}
	}

	out := userDetailJSON{User: userRow(*row), Exchanges: []userExchangeJSON{}, Violations: []violationJSON{}}
	for _, x := range exchanges { // newest first
		if e, ok := byExchange[x.ID]; ok {
			settings := s.admin.EffectiveSettings(x.Exchange)
			e.Title, e.Status = settings.Title, s.exchangeToJSON(x.Exchange).StatusTitle
			out.Exchanges = append(out.Exchanges, *e)
		}
	}
	for _, v := range violations {
		out.Violations = append(out.Violations, violationToJSON(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs []int64 `json:"user_ids"`
		Banned  bool    `json:"banned"`
		Reason  string  `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.admin.SetBanned(r.Context(), req.UserIDs, req.Banned, req.Reason)
	msg := "Разблокировано: " + strconv.Itoa(n) + "."
	if req.Banned {
		msg = "Заблокировано: " + strconv.Itoa(n) + ". Они не смогут добавлять детей и не попадут в распределение."
	}
	s.reply(w, msg, err)
}

func (s *Server) handleEditUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Phone     string `json:"phone"`
		PVZ       string `json:"pvz"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.reply(w, "Изменения сохранены.", s.admin.EditParent(r.Context(), id, req.FirstName, req.LastName, req.Phone, req.PVZ))
}

func (s *Server) handleEditChild(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Age       int    `json:"age"`
		Gender    string `json:"gender"`
		Wishes    string `json:"wishes"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.reply(w, "Изменения сохранены.", s.admin.EditChild(r.Context(), id, req.FirstName, req.LastName, req.Age, req.Gender, req.Wishes))
}

func (s *Server) handleDeleteChild(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.reply(w, "Ребёнок удалён.", s.admin.DeleteChild(r.Context(), id))
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs []int64 `json:"user_ids"`
		Text    string  `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.admin.SendMessage(r.Context(), req.UserIDs, req.Text)
	s.reply(w, "Отправлено: "+strconv.Itoa(n)+".", err)
}

func (s *Server) handleAddViolation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExchangeID int64  `json:"exchange_id"`
		UserID     int64  `json:"user_id"`
		Comment    string `json:"comment"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.reply(w, "Нарушение добавлено.", s.admin.AddViolation(r.Context(), req.ExchangeID, req.UserID, req.Comment))
}

func (s *Server) handleDismissViolation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Dismissed bool `json:"dismissed"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	msg := "Нарушение снято."
	if !req.Dismissed {
		msg = "Нарушение восстановлено."
	}
	err := s.admin.SetViolationDismissed(r.Context(), id, req.Dismissed)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Нарушение не найдено.")
		return
	}
	s.reply(w, msg, err)
}
