package render

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type LogLevel int

const (
	LogInfo LogLevel = iota
	LogWarning
	LogError
)

type LogEntry struct {
	Time    time.Time
	Level   LogLevel
	Message string
}

type Logger struct {
	entries   []LogEntry
	maxSize   int
	file      *os.File
	logToFile bool
}

func NewLogger(maxSize int, logToFile bool, logDir string) *Logger {
	l := &Logger{
		entries:   make([]LogEntry, 0, maxSize),
		maxSize:   maxSize,
		logToFile: logToFile,
	}

	if logToFile && logDir != "" {
		// Создаём директорию если нужно
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Printf("Failed to create log directory: %v\n", err)
			return l
		}

		// Открываем файл лога
		logPath := filepath.Join(logDir, "simulation.log")
		file, err := os.Create(logPath)
		if err != nil {
			fmt.Printf("Failed to create log file: %v\n", err)
			return l
		}

		l.file = file
	}

	return l
}

func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

func (l *Logger) Info(format string, args ...interface{}) {
	l.add(LogInfo, format, args...)
}

func (l *Logger) Warning(format string, args ...interface{}) {
	l.add(LogWarning, format, args...)
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.add(LogError, format, args...)
}

func (l *Logger) add(level LogLevel, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)

	entry := LogEntry{
		Time:    time.Now(),
		Level:   level,
		Message: msg,
	}

	l.entries = append(l.entries, entry)

	// Ограничиваем размер в памяти
	if len(l.entries) > l.maxSize {
		l.entries = l.entries[len(l.entries)-l.maxSize:]
	}

	// Пишем в файл
	if l.file != nil {
		levelStr := "INFO"
		switch level {
		case LogWarning:
			levelStr = "WARN"
		case LogError:
			levelStr = "ERROR"
		}

		timestamp := entry.Time.Format("2006-01-02 15:04:05")
		logLine := fmt.Sprintf("[%s] %s: %s\n", timestamp, levelStr, msg)
		l.file.WriteString(logLine)
		l.file.Sync()
	}
}

func (l *Logger) GetEntries() []LogEntry {
	return l.entries
}

func (l *Logger) Clear() {
	l.entries = l.entries[:0]
}
