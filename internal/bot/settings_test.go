package bot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Invalid settings must be refused before anything is written.
func TestUpdateSettingsValidation(t *testing.T) {
	b := &Bot{now: time.Now}
	day := func(d int) time.Time { return time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name string
		s    Settings
	}{
		{"zero budget", Settings{BudgetMax: 0, Currency: "BYN", OrderDeadline: day(13), DeliveryBy: day(25)}},
		{"huge budget", Settings{BudgetMax: 100001, Currency: "BYN", OrderDeadline: day(13), DeliveryBy: day(25)}},
		{"empty currency", Settings{BudgetMax: 25, Currency: "  ", OrderDeadline: day(13), DeliveryBy: day(25)}},
		{"long currency", Settings{BudgetMax: 25, Currency: "белорусских", OrderDeadline: day(13), DeliveryBy: day(25)}},
		{"no order date", Settings{BudgetMax: 25, Currency: "BYN", DeliveryBy: day(25)}},
		{"order after delivery", Settings{BudgetMax: 25, Currency: "BYN", OrderDeadline: day(26), DeliveryBy: day(25)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ae *ActionError
			if err := b.UpdateSettings(context.Background(), tt.s); !errors.As(err, &ae) {
				t.Fatalf("want ActionError, got %v", err)
			}
		})
	}
}
