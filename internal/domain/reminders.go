package domain

import "time"

type ReminderKind string

const (
	RemindGiverOrder         ReminderKind = "giver_order"
	RemindGiverArrivalCheck  ReminderKind = "giver_arrival_check"
	RemindGiverCodePending   ReminderKind = "giver_code_pending"
	RemindGiverCodeExpired   ReminderKind = "giver_code_expired"
	RemindRecipientPickup    ReminderKind = "recipient_pickup"
	RemindRecipientCodeToday ReminderKind = "recipient_code_today"
	RemindAdminStuck         ReminderKind = "admin_stuck"
)

type PickupState struct {
	Status          AssignmentStatus
	ExpectedDate    *time.Time
	ArrivedAt       *time.Time
	CodeSentAt      *time.Time
	CodeValidUntil  *time.Time
	CodeRequestedAt *time.Time
}

// CodeValid reports whether the last pickup code is still usable. WB pickup codes live until the end of the day they were issued.
func (s PickupState) CodeValid(now time.Time) bool {
	return s.CodeValidUntil != nil && now.Before(*s.CodeValidUntil)
}

func (s PickupState) CodeRequestPending() bool {
	return s.CodeRequestedAt != nil && (s.CodeSentAt == nil || s.CodeSentAt.Before(*s.CodeRequestedAt))
}

type ReminderPolicy struct {
	Location         *time.Location
	MorningHour      int
	EveningHour      int
	QuietHour        int
	CodePendingAfter time.Duration
	StuckAfter       time.Duration
}

// EndOfDay returns the first instant of the next local day, used as a code expiry.
func (p ReminderPolicy) EndOfDay(t time.Time) time.Time {
	return p.startOfDay(t).AddDate(0, 0, 1)
}

func (p ReminderPolicy) startOfDay(t time.Time) time.Time {
	l := t.In(p.Location)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, p.Location)
}

// DayKey is the local date used to send each reminder kind at most once per day.
func (p ReminderPolicy) DayKey(now time.Time) string {
	return now.In(p.Location).Format("2006-01-02")
}

// Due lists reminders that should go out for one assignment at the given moment.
// Callers dedupe by (assignment, kind, DayKey), so each kind fires at most once a day.
func (p ReminderPolicy) Due(now time.Time, s PickupState) []ReminderKind {
	hour := now.In(p.Location).Hour()
	if hour < p.MorningHour || hour >= p.QuietHour {
		return nil
	}
	today := p.startOfDay(now)
	var due []ReminderKind

	switch s.Status {
	case AssignmentAssigned:
		due = append(due, RemindGiverOrder)
	case AssignmentOrdered:
		if s.ExpectedDate != nil && dateBefore(*s.ExpectedDate, today) {
			due = append(due, RemindGiverArrivalCheck)
		}
	case AssignmentArrived:
		valid := s.CodeValid(now)
		pending := s.CodeRequestPending()

		if !valid && !pending {
			due = append(due, RemindRecipientPickup)
		}
		if valid && hour >= p.EveningHour {
			due = append(due, RemindRecipientCodeToday)
		}
		if pending && now.Sub(*s.CodeRequestedAt) >= p.CodePendingAfter {
			due = append(due, RemindGiverCodePending)
		}
		// Only the morning after: a code issued yesterday expires exactly at today's start.
		if !valid && !pending && s.CodeValidUntil != nil && !s.CodeValidUntil.Before(today) {
			due = append(due, RemindGiverCodeExpired)
		}
		if s.ArrivedAt != nil && now.Sub(*s.ArrivedAt) >= p.StuckAfter {
			due = append(due, RemindAdminStuck)
		}
	}
	return due
}

// dateBefore compares a DATE column value (stored as UTC midnight) with a local day start.
func dateBefore(date time.Time, localDay time.Time) bool {
	d := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, localDay.Location())
	return d.Before(localDay)
}
