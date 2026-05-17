package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// humanSize formats a byte count as a human-readable string.
func humanSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(bytes)
	for _, unit := range units {
		size /= 1024
		if size < 1024 {
			if size < 10 {
				return fmt.Sprintf("%.1f %s", size, unit)
			}
			return fmt.Sprintf("%.0f %s", size, unit)
		}
	}
	return fmt.Sprintf("%.0f TB", size)
}

// humanAge formats a time as a relative duration from now.
func humanAge(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	months := days / 30
	if months == 1 {
		return "1 month ago"
	}
	if months < 12 {
		return fmt.Sprintf("%d months ago", months)
	}
	years := months / 12
	if years == 1 {
		return "1 year ago"
	}
	return fmt.Sprintf("%d years ago", years)
}

// parseDuration parses a human-friendly duration string.
// Supported suffixes: h (hours), d (days), w (weeks), m (months of 30 days).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	suffix := s[len(s)-1:]
	numStr := s[:len(s)-1]

	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: number part %q is not an integer", s, numStr)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid duration %q: must be positive", s)
	}

	switch suffix {
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case "m":
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: unknown suffix %q (use h, d, w, or m)", s, suffix)
	}
}
