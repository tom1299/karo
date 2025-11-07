package e2e

import (
	"encoding/json"
	"strings"
)

type LogLevel string

const (
	LogLevelDebug   LogLevel = "debug"
	LogLevelInfo    LogLevel = "info"
	LogLevelWarning LogLevel = "warning"
	LogLevelError   LogLevel = "error"
	LogLevelUnknown LogLevel = "unknown"
)

func (l LogLevel) String() string {
	return string(l)
}

func ParseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "debug":
		return LogLevelDebug
	case "info":
		return LogLevelInfo
	case "warning", "warn":
		return LogLevelWarning
	case "error":
		return LogLevelError
	default:
		return LogLevelUnknown
	}
}

type LogEntry struct {
	Level        LogLevel `json:"level"`
	Timestamp    float64  `json:"ts"`
	Message      string   `json:"msg"`
	Controller   string   `json:"controller,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
	Name         string   `json:"name,omitempty"`
	ReconcileID  string   `json:"reconcileID,omitempty"`
	Operation    string   `json:"operation,omitempty"`
	OriginalLine string   `json:"-"`
}

type ExpectedLogEntry struct {
	Level   LogLevel
	Message string
}

type KaroLogs struct {
	entries []LogEntry
}

func parseLogLine(line string) LogEntry {
	var entry LogEntry
	entry.OriginalLine = line

	// Try to parse as JSON
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		// If parsing fails, store as a plain text log with the original line
		entry.Message = line
		entry.Level = LogLevelUnknown
	}

	return entry
}

func parseLogLines(lines []string) []LogEntry {
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		entries = append(entries, parseLogLine(line))
	}

	return entries
}

func GetKaroLogs(controllerManager *ControllerManager) *KaroLogs {
	lines := controllerManager.GetLogs()

	return &KaroLogs{
		entries: parseLogLines(lines),
	}
}

func (kl *KaroLogs) GetEntries() []LogEntry {
	return kl.entries
}

func (kl *KaroLogs) GetOriginalLines() []string {
	lines := make([]string, 0, len(kl.entries))
	for _, entry := range kl.entries {
		lines = append(lines, entry.OriginalLine)
	}

	return lines
}

func (kl *KaroLogs) ContainsLineWithMessage(message string) bool {
	for _, entry := range kl.entries {
		if strings.Contains(entry.Message, message) || strings.Contains(entry.OriginalLine, message) {
			return true
		}
	}

	return false
}

func (kl *KaroLogs) ContainSingleLineWithMessage(logLevel string, message string) bool {
	targetLevel := ParseLogLevel(logLevel)
	found := false
	for _, entry := range kl.entries {
		lineMatchesLevel := entry.Level == targetLevel
		if !lineMatchesLevel {
			continue
		}
		lineContainsMessage := strings.Contains(entry.Message, message) || strings.Contains(entry.OriginalLine, message)

		if lineContainsMessage && found {
			return false
		} else {
			found = true
		}
	}

	return found
}

func (kl *KaroLogs) ContainLogEntriesInSequence(expectedEntries []ExpectedLogEntry) bool {
	if len(expectedEntries) == 0 {
		return true
	}

	currentIndex := 0
	matchedIndices := make([]int, 0, len(expectedEntries))

	for i, entry := range kl.entries {
		expectedEntry := expectedEntries[currentIndex]
		levelMatches := entry.Level == expectedEntry.Level

		if !levelMatches {
			continue
		}

		messageMatches := strings.Contains(entry.Message, expectedEntry.Message) ||
			strings.Contains(entry.OriginalLine, expectedEntry.Message)

		if messageMatches {
			// Check if this log entry was already matched
			for _, matchedIdx := range matchedIndices {
				if matchedIdx == i {
					// This entry was already matched, fail
					return false
				}
			}

			matchedIndices = append(matchedIndices, i)
			currentIndex++

			if currentIndex >= len(expectedEntries) {
				// All expected entries found exactly once in sequence
				return true
			}
		}
	}

	// Not all expected entries were found
	return false
}

func containsAny(logLine string, messages []string) bool {
	for _, message := range messages {
		if strings.Contains(logLine, message) {
			return true
		}
	}

	return false
}

func (kl *KaroLogs) ContainNoErrorsOrStacktrace() bool {
	errorIndicators := []string{"error", "ERROR", "panic:", "goroutine "}
	for _, entry := range kl.entries {
		// Check level
		if entry.Level == LogLevelError {
			return false
		}
		// Check original line for error indicators
		if containsAny(entry.OriginalLine, errorIndicators) {
			return false
		}
	}

	return true
}
