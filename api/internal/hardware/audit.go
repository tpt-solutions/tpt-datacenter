// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package hardware

import (
	"strconv"
	"sync"
)

// AuditLog is a minimal append-only, in-memory AuditSink for the hardware
// management API. It mirrors api/internal/control.Store's audit log so power
// on/off/cycle, boot-override, and power-cap actions are recorded with the
// same guarantees as manual control overrides, rather than silently dropped.
type AuditLog struct {
	mu      sync.RWMutex
	entries []AuditEntry
	cap     int
	seq     uint64
}

// NewAuditLog builds an empty audit log retaining the most recent entries.
func NewAuditLog() *AuditLog {
	return &AuditLog{cap: 1000}
}

// Record appends an audit entry, trimming the oldest once the cap is reached.
func (a *AuditLog) Record(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	entry.ID = "aud-" + strconv.FormatUint(a.seq, 10)
	a.entries = append(a.entries, entry)
	if len(a.entries) > a.cap {
		a.entries = a.entries[len(a.entries)-a.cap:]
	}
}

// Recent returns up to limit most-recent entries, newest first.
func (a *AuditLog) Recent(limit int) []AuditEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if limit <= 0 || limit > len(a.entries) {
		limit = len(a.entries)
	}
	out := make([]AuditEntry, 0, limit)
	for i := len(a.entries) - 1; i >= len(a.entries)-limit; i-- {
		out = append(out, a.entries[i])
	}
	return out
}
