package main

import (
	"testing"
)

func TestParseSize(t *testing.T) {
	validCases := []struct {
		name  string
		input string
		want  int64
	}{
		{"bytes", "100B", 100},
		{"kilobytes", "1KB", 1024},
		{"megabytes", "5MB", 5242880},
		{"gigabytes", "2GB", 2147483648},
		{"terabytes", "1TB", 1099511627776},
		{"case insensitive mb lower", "100mb", 5242880 / 5 * 100},
		{"case insensitive kb lower", "1kb", 1024},
		{"case insensitive mixed", "5Mb", 5242880},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSize(tc.input)
			if err != nil {
				t.Fatalf("parseSize(%q) returned unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("parseSize(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}

	// Verify case insensitivity produces identical results.
	t.Run("case insensitive mb matches MB", func(t *testing.T) {
		lower, err1 := parseSize("100mb")
		upper, err2 := parseSize("100MB")
		if err1 != nil || err2 != nil {
			t.Fatalf("unexpected error: lower=%v, upper=%v", err1, err2)
		}
		if lower != upper {
			t.Errorf("parseSize(\"100mb\") = %d, parseSize(\"100MB\") = %d; want equal", lower, upper)
		}
	})

	t.Run("case insensitive kb matches KB", func(t *testing.T) {
		lower, err1 := parseSize("1kb")
		upper, err2 := parseSize("1KB")
		if err1 != nil || err2 != nil {
			t.Fatalf("unexpected error: lower=%v, upper=%v", err1, err2)
		}
		if lower != upper {
			t.Errorf("parseSize(\"1kb\") = %d, parseSize(\"1KB\") = %d; want equal", lower, upper)
		}
	})

	errorCases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no suffix", "100"},
		{"zero value", "0MB"},
		{"no number", "MB"},
		{"unknown suffix", "100PB"},
		{"not a number", "abc"},
		{"negative value", "-5MB"},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSize(tc.input)
			if err == nil {
				t.Errorf("parseSize(%q) = %d, want error", tc.input, got)
			}
		})
	}
}
