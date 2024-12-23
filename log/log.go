// File: log/log.go
package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var log zerolog.Logger

// LogCallback is the type for log callback functions
type LogCallback func(level zerolog.Level, msg string, fields map[string]interface{})

// CallbackWriter wraps an io.Writer and calls the callback for each log entry
type CallbackWriter struct {
	writer   io.Writer
	callback LogCallback
}

type WebhookMessage struct {
	Content string `json:"content"`
}

// Write implements io.Writer
func (w *CallbackWriter) Write(p []byte) (n int, err error) {
	// Parse the JSON log entry
	var entry map[string]interface{}
	if err := json.Unmarshal(p, &entry); err == nil {
		level, _ := entry["level"].(string)
		msg, _ := entry["message"].(string)

		// Convert zerolog level string to Level type
		var logLevel zerolog.Level
		switch level {
		case "debug":
			logLevel = zerolog.DebugLevel
		case "info":
			logLevel = zerolog.InfoLevel
		case "warn":
			logLevel = zerolog.WarnLevel
		case "error":
			logLevel = zerolog.ErrorLevel
		case "fatal":
			logLevel = zerolog.FatalLevel
		}

		// Remove standard fields from the map before passing to callback
		delete(entry, "level")
		delete(entry, "message")
		delete(entry, "time")
		delete(entry, "caller")

		// Call the callback
		if w.callback != nil {
			w.callback(logLevel, msg, entry)
		}
	}

	// Write to the underlying writer
	return w.writer.Write(p)
}

// SetLogCallback sets a callback function that will be called for each log message
func SetLogCallback(callback LogCallback) {
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	}

	callbackWriter := &CallbackWriter{
		writer:   consoleWriter,
		callback: callback,
	}

	log = zerolog.New(callbackWriter).With().Timestamp().Caller().Logger()
}

// Helper function to format fields
func formatFields(fields map[string]interface{}) string {
	if len(fields) == 0 {
		return "none"
	}
	var fieldStrings []string
	for key, value := range fields {
		// Handle different value types appropriately
		var valueStr string
		switch v := value.(type) {
		case string:
			valueStr = v
		case int, int64, float64:
			valueStr = fmt.Sprintf("%v", v)
		case error:
			valueStr = v.Error()
		case nil:
			valueStr = "null"
		default:
			// For complex types, use JSON marshaling
			bytes, err := json.Marshal(v)
			if err != nil {
				valueStr = fmt.Sprintf("%v", v)
			} else {
				valueStr = string(bytes)
			}
		}
		fieldStrings = append(fieldStrings, fmt.Sprintf("  %s: %s", key, valueStr))
	}
	return strings.Join(fieldStrings, "\n")
}

func init() {
	log = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	}).With().Timestamp().Caller().Logger()

	SetLogCallback(func(level zerolog.Level, msg string, fields map[string]interface{}) {
		if os.Getenv("ENVIORNMENT") == "dev" {
			return
		}
		// form the log message
		formatedFields := formatFields(fields)
		fieldMsg := ""
		if formatedFields != "none" {
			fieldMsg = fmt.Sprintf("\n\n# Fields: \n%s", formatedFields)
		}
		logMsg := fmt.Sprintf("```yaml\n# %s Log Entry\n  %s: %s%s```",
			strings.ToUpper(level.String()),
			level,
			msg,
			fieldMsg,
		)
		whMsg := WebhookMessage{
			Content: logMsg,
		}
		jsonBody, err := json.Marshal(whMsg)
		if err != nil {
			println("Failed to marshal webhook message", err.Error())
			return
		}
		// send a post request to the webhook url
		resp, err := http.Post(os.Getenv("WEBHOOK_URL"), "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			println("Failed to send log message to webhook", err.Error())
			return
		}
		defer resp.Body.Close()

		// if the response is not 200, return an error
		if resp.StatusCode != 204 {
			body, _ := io.ReadAll(resp.Body)
			println("Failed to send log message to webhook", resp.Status, string(body))
			return
		}
	})
}

// Fields is a map of field names to values
type Fields map[string]interface{}

// Info returns a new Info event logger
func Info() *zerolog.Event {
	return log.Info()
}

// Error returns a new Error event logger
func Error() *zerolog.Event {
	return log.Error()
}

// Debug returns a new Debug event logger
func Debug() *zerolog.Event {
	return log.Debug()
}

// Warn returns a new Warn event logger
func Warn() *zerolog.Event {
	return log.Warn()
}

// Fatal returns a new Fatal event logger
func Fatal() *zerolog.Event {
	return log.Fatal()
}
