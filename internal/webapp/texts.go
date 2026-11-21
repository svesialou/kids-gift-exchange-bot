package webapp

import (
	"errors"
	"net/http"

	"github.com/svesialou/kids-gift-exchange-bot/internal/texts"
)

type textJSON struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Title   string   `json:"title"`
	Value   string   `json:"value"`
	Default string   `json:"default"`
	Vars    []string `json:"vars"`
	Button  bool     `json:"button"`
	Changed bool     `json:"changed"`
}

func (s *Server) handleTexts(w http.ResponseWriter, _ *http.Request) {
	entries := s.admin.Texts().Entries()
	out := make([]textJSON, 0, len(entries))
	for _, e := range entries {
		vars := e.Vars
		if vars == nil {
			vars = []string{}
		}
		out = append(out, textJSON{Key: e.Key, Group: e.Group, Title: e.Title, Value: e.Value, Default: e.Default,
			Vars: vars, Button: e.Button, Changed: e.Changed})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value string `json:"value"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.replyText(w, "Текст сохранён.", s.admin.Texts().Set(r.Context(), r.PathValue("key"), req.Value))
}

func (s *Server) handleResetText(w http.ResponseWriter, r *http.Request) {
	s.replyText(w, "Вернули исходный текст.", s.admin.Texts().Reset(r.Context(), r.PathValue("key")))
}

func (s *Server) replyText(w http.ResponseWriter, msg string, err error) {
	var inv *texts.ErrInvalid
	if errors.As(err, &inv) {
		writeError(w, http.StatusConflict, inv.Msg)
		return
	}
	s.reply(w, msg, err)
}
