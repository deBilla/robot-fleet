package store

import "time"

// RobotRecord represents a robot record in the database.
type RobotRecord struct {
	ID               string
	Name             string
	Model            string // hardware model (e.g. "humanoid-v1")
	Status           string
	PosX             float64
	PosY             float64
	PosZ             float64
	BatteryLevel     float64
	LastSeen         time.Time
	RegisteredAt     time.Time
	TenantID         string
	Metadata         map[string]string
	InferenceModelID string // assigned inference model from model_registry (empty = server default)
}

// CommandAuditEntry represents a row in the command_audit table.
type CommandAuditEntry struct {
	ID             int64
	CommandID      string
	RobotID        string
	TenantID       string
	CommandType    string
	Payload        []byte
	Status         string
	Instruction    string
	IdempotencyKey string
	CreatedAt      time.Time
}
