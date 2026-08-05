package tasks

import "time"

type MemoryJobMessage struct {
	JobID     string    `json:"job_id"`
	Type      string    `json:"type"`
	UserID    uint64    `json:"user_id"`
	MemoryID  uint64    `json:"memory_id,omitempty"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`
}
