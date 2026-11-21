// Package texts holds every text participants see in the bot, with organizer overrides.
package texts

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

const (
	maxTextLen   = 3000
	maxButtonLen = 64
)

var (
	boldRe        = regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
	codeRe        = regexp.MustCompile("`([^`\n]+)`")
	placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)
	byKey         = func() map[string]Def {
		m := make(map[string]Def, len(Catalog))
		for _, d := range Catalog {
			m[d.Key] = d
		}
		return m
	}()
)

// ErrInvalid is returned for an override organizers must fix; its message is shown to them as is.
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

type Storage interface {
	TextOverrides(ctx context.Context) (map[string]string, error)
	SaveText(ctx context.Context, key, value string) error
	DeleteText(ctx context.Context, key string) error
}

type Service struct {
	storage   Storage
	overrides atomic.Pointer[map[string]string]
}

func New(storage Storage) *Service {
	s := &Service{storage: storage}
	empty := map[string]string{}
	s.overrides.Store(&empty)
	return s
}

// Load refreshes overrides from storage. The bot is the only writer, so the cache stays in sync.
func (s *Service) Load(ctx context.Context) error {
	m, err := s.storage.TextOverrides(ctx)
	if err != nil {
		return err
	}
	s.overrides.Store(&m)
	return nil
}

func (s *Service) template(key string) string {
	if v, ok := (*s.overrides.Load())[key]; ok {
		return v
	}
	if d, ok := byKey[key]; ok {
		return d.Default
	}
	return key
}

// Render returns Telegram HTML: the template is escaped, **bold** and `code` become tags,
// then {placeholders} are replaced with escaped values given as key, value pairs.
func (s *Service) Render(key string, kv ...string) string {
	out := html.EscapeString(s.template(key))
	out = boldRe.ReplaceAllString(out, "<b>$1</b>")
	out = codeRe.ReplaceAllString(out, "<code>$1</code>")
	return substitute(out, kv, html.EscapeString)
}

// Plain returns a button label: no markup, values inserted as is.
func (s *Service) Plain(key string, kv ...string) string {
	return substitute(s.template(key), kv, func(v string) string { return v })
}

func substitute(text string, kv []string, escape func(string) string) string {
	if len(kv) == 0 {
		return text
	}
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+kv[i]+"}", escape(kv[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// MenuKey returns the key of the menu button whose current label equals text.
func (s *Service) MenuKey(text string) (string, bool) {
	for _, d := range Catalog {
		if d.Menu && s.Plain(d.Key) == text {
			return d.Key, true
		}
	}
	return "", false
}

type Entry struct {
	Def
	Value   string
	Changed bool
}

func (s *Service) Entries() []Entry {
	overrides := *s.overrides.Load()
	out := make([]Entry, 0, len(Catalog))
	for _, d := range Catalog {
		v, changed := overrides[d.Key]
		if !changed {
			v = d.Default
		}
		out = append(out, Entry{Def: d, Value: v, Changed: changed})
	}
	return out
}

func (s *Service) Set(ctx context.Context, key, value string) error {
	d, ok := byKey[key]
	if !ok {
		return &ErrInvalid{Msg: "Такого текста нет."}
	}
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if err := s.validate(d, value); err != nil {
		return err
	}
	if value == d.Default {
		return s.Reset(ctx, key)
	}
	if err := s.storage.SaveText(ctx, key, value); err != nil {
		return err
	}
	return s.Load(ctx)
}

func (s *Service) Reset(ctx context.Context, key string) error {
	if _, ok := byKey[key]; !ok {
		return &ErrInvalid{Msg: "Такого текста нет."}
	}
	if err := s.storage.DeleteText(ctx, key); err != nil {
		return err
	}
	return s.Load(ctx)
}

func (s *Service) validate(d Def, value string) error {
	limit := maxTextLen
	if d.Button {
		limit = maxButtonLen
	}
	switch n := utf8.RuneCountInString(value); {
	case n == 0:
		return &ErrInvalid{Msg: "Текст не может быть пустым."}
	case n > limit:
		return &ErrInvalid{Msg: fmt.Sprintf("Слишком длинно: максимум %d символов.", limit)}
	case d.Button && strings.Contains(value, "\n"):
		return &ErrInvalid{Msg: "Текст кнопки должен быть в одну строку."}
	case strings.HasPrefix(value, "/"):
		return &ErrInvalid{Msg: "Текст не может начинаться с «/»: Telegram примет его за команду."}
	}
	allowed := map[string]bool{}
	for _, v := range d.Vars {
		allowed[v] = true
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(value, -1) {
		if !allowed[m[1]] {
			if len(d.Vars) == 0 {
				return &ErrInvalid{Msg: fmt.Sprintf("Подстановка {%s} здесь недоступна: в этом тексте подстановок нет.", m[1])}
			}
			return &ErrInvalid{Msg: fmt.Sprintf("Подстановка {%s} здесь недоступна. Можно: {%s}.", m[1], strings.Join(d.Vars, "}, {"))}
		}
	}
	if d.Menu {
		for _, other := range Catalog {
			if other.Menu && other.Key != d.Key && s.Plain(other.Key) == value {
				return &ErrInvalid{Msg: fmt.Sprintf("Такая же надпись уже у кнопки «%s». Кнопки меню должны различаться.", other.Title)}
			}
		}
	}
	return nil
}
