package api

import (
	"strings"
	"sync"
)

// MemoryLogger keeps a bounded in-memory copy of aggregated recent log lines.
type MemoryLogger struct {
	mu     sync.RWMutex
	logs   []LogEntry
	maxLog int
}

// NewMemoryLogger creates a logger that retains up to maxLines distinct consecutive entries.
func NewMemoryLogger(maxLines int) *MemoryLogger {
	return &MemoryLogger{
		logs:   make([]LogEntry, 0, maxLines),
		maxLog: maxLines,
	}
}

// LogEntry represents a single or repeated log message.
type LogEntry struct {
	Timestamp string
	Message   string
	Count     int
}

// Write appends a log entry, aggregating it if it matches the last entry (ignoring timestamp).
func (m *MemoryLogger) Write(p []byte) (n int, err error) {
	raw := string(p)
	timestamp := ""
	msg := raw

	// Standard Go log format is "2026/01/02 15:04:05 Message"
	// The prefix is 20 characters long.
	if len(raw) > 20 && raw[4] == '/' && raw[7] == '/' && raw[13] == ':' {
		timestamp = raw[:19]
		msg = strings.TrimSpace(raw[20:])
	} else {
		msg = strings.TrimSpace(raw)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Aggregate if message is identical to the last entry (ignoring timestamp)
	if len(m.logs) > 0 && m.logs[len(m.logs)-1].Message == msg {
		m.logs[len(m.logs)-1].Count++
		m.logs[len(m.logs)-1].Timestamp = timestamp
		return len(p), nil
	}

	// Not a duplicate, add a new entry
	if len(m.logs) >= m.maxLog {
		m.logs = m.logs[1:]
	}
	m.logs = append(m.logs, LogEntry{
		Timestamp: timestamp,
		Message:   msg,
		Count:     1,
	})
	return len(p), nil
}

// GetAggregatedLogs returns the history of aggregated logs.
func (m *MemoryLogger) GetAggregatedLogs() []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy
	res := make([]LogEntry, len(m.logs))
	copy(res, m.logs)
	return res
}
