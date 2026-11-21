package bot

import (
	"testing"
	"time"
)

func TestParseDayMonth(t *testing.T) {
	now := time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"15.12", "2026-12-15", true},
		{"5.1", "2027-01-05", true},
		{"05/01", "2027-01-05", true},
		{"01.12", "2026-12-01", true},
		{"32.12", "", false},
		{"завтра", "", false},
	}
	for _, tt := range tests {
		got, ok := parseDayMonth(tt.in, now)
		if ok != tt.ok || (ok && got.Format("2006-01-02") != tt.want) {
			t.Errorf("%q: want %s/%v, got %s/%v", tt.in, tt.want, tt.ok, got.Format("2006-01-02"), ok)
		}
	}
}
