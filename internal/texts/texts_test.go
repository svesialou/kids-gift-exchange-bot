package texts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type memStorage struct{ m map[string]string }

func (s *memStorage) TextOverrides(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}
func (s *memStorage) SaveText(_ context.Context, k, v string) error { s.m[k] = v; return nil }
func (s *memStorage) DeleteText(_ context.Context, k string) error  { delete(s.m, k); return nil }

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	menuLabels := map[string]bool{}
	for _, d := range Catalog {
		if seen[d.Key] {
			t.Fatalf("duplicate key %s", d.Key)
		}
		seen[d.Key] = true
		if d.Title == "" || d.Group == "" || d.Default == "" {
			t.Fatalf("%s: title, group and default are required", d.Key)
		}
		s := New(&memStorage{m: map[string]string{}})
		if err := s.validate(d, d.Default); err != nil {
			t.Fatalf("%s: default fails its own validation: %v", d.Key, err)
		}
		if d.Menu {
			if menuLabels[d.Default] {
				t.Fatalf("%s: menu label repeats", d.Key)
			}
			menuLabels[d.Default] = true
		}
	}
}

func TestRenderEscapesAndFormats(t *testing.T) {
	s := New(&memStorage{m: map[string]string{}})
	got := s.Render("recv.code", "child", "<Маша>", "code", "12**34", "pvz", "ул. Ленина & Ко")
	for _, want := range []string{"<b>&lt;Маша&gt;</b>", "<code>12**34</code>", "ул. Ленина &amp; Ко", "<b>только сегодня</b>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in %q", want, got)
		}
	}
	if strings.Contains(got, "{") {
		t.Fatalf("placeholder left unreplaced: %q", got)
	}
}

func TestOverridesValidationAndReset(t *testing.T) {
	ctx := context.Background()
	st := &memStorage{m: map[string]string{}}
	s := New(st)

	if err := s.Set(ctx, "give.thanks", "Спасибо за подарок для **{child}** <3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Render("give.thanks", "child", "Ваня"); got != "Спасибо за подарок для <b>Ваня</b> &lt;3" {
		t.Fatalf("override not used: %q", got)
	}

	var inv *ErrInvalid
	cases := []struct{ key, value string }{
		{"give.thanks", "Спасибо, {parent}!"},
		{"common.error", "Ошибка {child}"},
		{"btn.received", "Забрал\nдва"},
		{"btn.received", ""},
		{"btn.give", "👥 Участники"},
		{"reg.ask_first", "/start"},
		{"nope", "x"},
	}
	for _, c := range cases {
		if err := s.Set(ctx, c.key, c.value); !errors.As(err, &inv) {
			t.Fatalf("%s=%q: want ErrInvalid, got %v", c.key, c.value, err)
		}
	}

	if err := s.Set(ctx, "btn.give", "🎁 Подарить"); err != nil {
		t.Fatal(err)
	}
	if key, ok := s.MenuKey("🎁 Подарить"); !ok || key != "btn.give" {
		t.Fatalf("renamed menu button not recognised: %q %v", key, ok)
	}
	if _, ok := s.MenuKey("🎁 Кому я дарю"); ok {
		t.Fatal("the old label must stop working")
	}

	if err := s.Reset(ctx, "give.thanks"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.m["give.thanks"]; ok {
		t.Fatal("reset must delete the override")
	}
	if err := s.Set(ctx, "btn.give", "🎁 Кому я дарю"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.m["btn.give"]; ok {
		t.Fatal("saving the default must drop the override")
	}
}
