package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestReminderPolicyDue(t *testing.T) {
	minsk, err := time.LoadLocation("Europe/Minsk")
	if err != nil {
		t.Fatal(err)
	}
	p := ReminderPolicy{
		Location:         minsk,
		MorningHour:      10,
		EveningHour:      19,
		QuietHour:        22,
		CodePendingAfter: 2 * time.Hour,
		StuckAfter:       96 * time.Hour,
	}
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 12, day, hour, minute, 0, 0, minsk) }
	ptr := func(t time.Time) *time.Time { return &t }
	dateCol := func(day int) *time.Time { return ptr(time.Date(2026, 12, day, 0, 0, 0, 0, time.UTC)) }

	tests := []struct {
		name  string
		now   time.Time
		state PickupState
		want  []ReminderKind
	}{
		{
			name:  "quiet night hours",
			now:   at(15, 23, 0),
			state: PickupState{Status: AssignmentAssigned},
		},
		{
			name:  "before morning hour",
			now:   at(15, 9, 59),
			state: PickupState{Status: AssignmentAssigned},
		},
		{
			name:  "not ordered yet",
			now:   at(15, 10, 0),
			state: PickupState{Status: AssignmentAssigned},
			want:  []ReminderKind{RemindGiverOrder},
		},
		{
			name:  "ordered, expected date not passed",
			now:   at(15, 12, 0),
			state: PickupState{Status: AssignmentOrdered, ExpectedDate: dateCol(15)},
		},
		{
			name:  "ordered, expected date passed",
			now:   at(16, 12, 0),
			state: PickupState{Status: AssignmentOrdered, ExpectedDate: dateCol(15)},
			want:  []ReminderKind{RemindGiverArrivalCheck},
		},
		{
			name:  "arrived, no code yet",
			now:   at(16, 11, 0),
			state: PickupState{Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0))},
			want:  []ReminderKind{RemindRecipientPickup},
		},
		{
			name: "code requested recently",
			now:  at(16, 11, 0),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeRequestedAt: ptr(at(16, 10, 0)),
			},
		},
		{
			name: "code requested long ago",
			now:  at(16, 12, 30),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeRequestedAt: ptr(at(16, 10, 0)),
			},
			want: []ReminderKind{RemindGiverCodePending},
		},
		{
			name: "valid code during the day",
			now:  at(16, 15, 0),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeRequestedAt: ptr(at(16, 10, 0)), CodeSentAt: ptr(at(16, 11, 0)), CodeValidUntil: ptr(at(17, 0, 0)),
			},
		},
		{
			name: "valid code in the evening",
			now:  at(16, 19, 0),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeSentAt: ptr(at(16, 11, 0)), CodeValidUntil: ptr(at(17, 0, 0)),
			},
			want: []ReminderKind{RemindRecipientCodeToday},
		},
		{
			name: "code expired overnight",
			now:  at(17, 10, 0),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeSentAt: ptr(at(16, 11, 0)), CodeValidUntil: ptr(at(17, 0, 0)),
			},
			want: []ReminderKind{RemindRecipientPickup, RemindGiverCodeExpired},
		},
		{
			name: "code expired two days ago is not repeated to giver",
			now:  at(18, 10, 0),
			state: PickupState{
				Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0)),
				CodeSentAt: ptr(at(16, 11, 0)), CodeValidUntil: ptr(at(17, 0, 0)),
			},
			want: []ReminderKind{RemindRecipientPickup},
		},
		{
			name:  "parcel waits too long",
			now:   at(20, 10, 0),
			state: PickupState{Status: AssignmentArrived, ArrivedAt: ptr(at(16, 8, 0))},
			want:  []ReminderKind{RemindRecipientPickup, RemindAdminStuck},
		},
		{
			name:  "received",
			now:   at(20, 10, 0),
			state: PickupState{Status: AssignmentReceived, ArrivedAt: ptr(at(16, 8, 0))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Due(tt.now, tt.state)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, got)
			}
		})
	}
}

func TestReminderPolicyEndOfDay(t *testing.T) {
	minsk, _ := time.LoadLocation("Europe/Minsk")
	p := ReminderPolicy{Location: minsk}
	// 22:30 UTC is already 01:30 next day in Minsk.
	got := p.EndOfDay(time.Date(2026, 12, 16, 22, 30, 0, 0, time.UTC))
	want := time.Date(2026, 12, 18, 0, 0, 0, 0, minsk)
	if !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}
