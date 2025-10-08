package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	// LogLevelSilent disables all logging
	LogLevelSilent LogLevel = iota
	// LogLevelError shows only errors
	LogLevelError
	// LogLevelWarn shows warnings and errors
	LogLevelWarn
	// LogLevelInfo shows info, warnings, and errors (verbose mode)
	LogLevelInfo
	// LogLevelDebug shows all logs including debug information
	LogLevelDebug
)

var levelNames = map[LogLevel]string{
	LogLevelSilent: "SILENT",
	LogLevelError:  "ERROR",
	LogLevelWarn:   "WARN",
	LogLevelInfo:   "INFO",
	LogLevelDebug:  "DEBUG",
}

// Logger provides structured logging with levels
type Logger struct {
	level  LogLevel
	output io.Writer
}

var defaultLogger = &Logger{
	level:  LogLevelError,
	output: os.Stderr,
}

// SetLogLevel sets the global log level
func SetLogLevel(level LogLevel) {
	defaultLogger.level = level
}

// GetLogLevel returns the current log level
func GetLogLevel() LogLevel {
	return defaultLogger.level
}

// log writes a log message if the level is enabled
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level > l.level {
		return
	}

	timestamp := time.Now().Format("15:04:05.000")
	levelName := levelNames[level]
	message := fmt.Sprintf(format, args...)

	// Redact sensitive information
	message = redactSensitive(message)

	fmt.Fprintf(l.output, "[%s] %s: %s\n", timestamp, levelName, message)
}

// Debug logs a debug message
func Debug(format string, args ...interface{}) {
	defaultLogger.log(LogLevelDebug, format, args...)
}

// Info logs an info message
func Info(format string, args ...interface{}) {
	defaultLogger.log(LogLevelInfo, format, args...)
}

// Warn logs a warning message
func Warn(format string, args ...interface{}) {
	defaultLogger.log(LogLevelWarn, format, args...)
}

// Error logs an error message
func Error(format string, args ...interface{}) {
	defaultLogger.log(LogLevelError, format, args...)
}

// redactSensitive removes sensitive information from log messages
func redactSensitive(message string) string {
	// Redact Authorization headers
	if strings.Contains(message, "Authorization:") {
		message = strings.ReplaceAll(message, "Authorization: Bearer ", "Authorization: Bearer ***")
		message = strings.ReplaceAll(message, "Authorization: Basic ", "Authorization: Basic ***")
	}

	// Redact tokens in URLs
	if strings.Contains(message, "token=") {
		parts := strings.Split(message, "token=")
		if len(parts) > 1 {
			for i := 1; i < len(parts); i++ {
				endIdx := strings.IndexAny(parts[i], "& \n")
				if endIdx == -1 {
					endIdx = len(parts[i])
				}
				parts[i] = "***" + parts[i][endIdx:]
			}
			message = strings.Join(parts, "token=")
		}
	}

	// Redact password in credential strings
	if strings.Contains(message, "password") || strings.Contains(message, "PASSWORD") {
		message = strings.ReplaceAll(message, "password=", "password=***")
		message = strings.ReplaceAll(message, "PASSWORD=", "PASSWORD=***")
	}

	return message
}
