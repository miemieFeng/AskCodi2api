package service

import (
	"fmt"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

type Logger struct {
	mu     sync.RWMutex
	logs   []LogEntry
	maxLen int
}

func NewLogger(maxLen int) *Logger {
	return &Logger{
		logs:   make([]LogEntry, 0, maxLen),
		maxLen: maxLen,
	}
}

func (l *Logger) Log(message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	entry := LogEntry{Timestamp: timestamp, Message: message}

	fmt.Printf("[%s] %s\n", timestamp, message)

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.logs) >= l.maxLen {
		l.logs = l.logs[1:]
	}
	l.logs = append(l.logs, entry)
}

func (l *Logger) GetLogs() []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]LogEntry, len(l.logs))
	copy(result, l.logs)
	return result
}
