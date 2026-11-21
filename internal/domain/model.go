package domain

type ExchangeStatus string

const (
	ExchangeDraft       ExchangeStatus = "draft"
	ExchangeOpen        ExchangeStatus = "registration_open"
	ExchangeClosed      ExchangeStatus = "registration_closed"
	ExchangeDistributed ExchangeStatus = "distributed"
	ExchangeCompleted   ExchangeStatus = "completed"
)

type AssignmentStatus string

const (
	AssignmentAssigned AssignmentStatus = "assigned"
	AssignmentOrdered  AssignmentStatus = "ordered"
	AssignmentArrived  AssignmentStatus = "arrived"
	AssignmentReceived AssignmentStatus = "received"
)
