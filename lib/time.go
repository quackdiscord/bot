package lib

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTime parses a string into a time.Time object
func ParseTime(durationStr string) (time.Time, error) {
	// Split duration string into value and unit
	durationStr = strings.TrimSpace(durationStr)
	valueStr := durationStr
	unit := "s" // Default to seconds if no unit is provided

	for i, char := range durationStr {
		if char < '0' || char > '9' {
			valueStr = durationStr[:i]
			unit = durationStr[i:]
			// lowercase the unit
			unit = strings.ToLower(strings.TrimSpace(unit))
			break
		}
	}

	// Parse the value
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse value: %v", err)
	}

	// Determine the duration based on the unit
	var duration time.Duration
	switch unit {
	case "s":
		duration = time.Second * time.Duration(value)
	case "m":
		duration = time.Minute * time.Duration(value)
	case "h":
		duration = time.Hour * time.Duration(value)
	case "d":
		duration = time.Hour * 24 * time.Duration(value)
	case "w":
		duration = time.Hour * 24 * 7 * time.Duration(value)
	case "sec":
		duration = time.Second * time.Duration(value)
	case "min":
		duration = time.Minute * time.Duration(value)
	case "hr":
		duration = time.Hour * time.Duration(value)
	case "day":
		duration = time.Hour * 24 * time.Duration(value)
	case "week":
		duration = time.Hour * 24 * 7 * time.Duration(value)
	case "second":
		duration = time.Second * time.Duration(value)
	case "minute":
		duration = time.Minute * time.Duration(value)
	case "hour":
		duration = time.Hour * time.Duration(value)
	case "days":
		duration = time.Hour * 24 * time.Duration(value)
	case "weeks":
		duration = time.Hour * 24 * 7 * time.Duration(value)
	case "seconds":
		duration = time.Second * time.Duration(value)
	case "minutes":
		duration = time.Minute * time.Duration(value)
	case "hours":
		duration = time.Hour * time.Duration(value)
	default:
		return time.Time{}, fmt.Errorf("unsupported unit: %s", unit)
	}

	// Calculate the future time
	futureTime := time.Now().Add(duration)
	return futureTime, nil
}
