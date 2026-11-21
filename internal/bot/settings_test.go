package bot

import (
	"errors"
	"testing"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
)

func TestValidateSettings(t *testing.T) {
	b := &Bot{now: time.Now, cfg: config.Config{Location: time.UTC}}
	day := func(d int) *time.Time { t := time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC); return &t }
	valid := func() Settings {
		return Settings{Title: "Новый год", BudgetMax: 25, Currency: "BYN",
			RegistrationOpensAt: day(1), RegistrationClosesAt: day(5), OrderDeadline: *day(13), DeliveryBy: *day(25)}
	}
	if s := valid(); b.validateSettings(&s) != nil {
		t.Fatalf("baseline settings must be valid: %v", b.validateSettings(&s))
	}

	tests := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"no title", func(s *Settings) { s.Title = "  " }},
		{"zero budget", func(s *Settings) { s.BudgetMax = 0 }},
		{"huge budget", func(s *Settings) { s.BudgetMax = 100001 }},
		{"empty currency", func(s *Settings) { s.Currency = " " }},
		{"long currency", func(s *Settings) { s.Currency = "белорусских" }},
		{"no registration start", func(s *Settings) { s.RegistrationOpensAt = nil }},
		{"no registration end", func(s *Settings) { s.RegistrationClosesAt = nil }},
		{"no send-by date", func(s *Settings) { s.OrderDeadline = time.Time{} }},
		{"no end date", func(s *Settings) { s.DeliveryBy = time.Time{} }},
		{"registration closes before it opens", func(s *Settings) { s.RegistrationOpensAt, s.RegistrationClosesAt = day(5), day(4) }},
		{"registration closes after send-by date", func(s *Settings) { s.RegistrationClosesAt = day(14) }},
		{"send-by after the end", func(s *Settings) { s.OrderDeadline = *day(26) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.mutate(&s)
			var ae *ActionError
			if err := b.validateSettings(&s); !errors.As(err, &ae) {
				t.Fatalf("want ActionError, got %v", err)
			}
		})
	}
}
