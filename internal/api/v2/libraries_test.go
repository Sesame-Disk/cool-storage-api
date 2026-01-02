package v2

import (
	"testing"
)

// TestFormatSize tests the human-readable size formatter
func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		// Bytes
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},

		// Kilobytes
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{2048, "2.0 KB"},
		{10240, "10.0 KB"},
		{1048575, "1024.0 KB"}, // Just under 1 MB

		// Megabytes
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
		{10485760, "10.0 MB"},
		{104857600, "100.0 MB"},
		{1073741823, "1024.0 MB"}, // Just under 1 GB

		// Gigabytes
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
		{10737418240, "10.0 GB"},
		{107374182400, "100.0 GB"},

		// Terabytes
		{1099511627776, "1.0 TB"},
		{1649267441664, "1.5 TB"},
		{10995116277760, "10.0 TB"},

		// Petabytes
		{1125899906842624, "1.0 PB"},

		// Exabytes
		{1152921504606846976, "1.0 EB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

// TestFormatSizeEdgeCases tests edge cases for size formatting
func TestFormatSizeEdgeCases(t *testing.T) {
	// Test boundary between units
	t.Run("KB boundary", func(t *testing.T) {
		below := formatSize(1023)
		at := formatSize(1024)

		if below != "1023 B" {
			t.Errorf("1023 bytes should be '1023 B', got %q", below)
		}
		if at != "1.0 KB" {
			t.Errorf("1024 bytes should be '1.0 KB', got %q", at)
		}
	})

	t.Run("MB boundary", func(t *testing.T) {
		below := formatSize(1048575)
		at := formatSize(1048576)

		if below != "1024.0 KB" {
			t.Errorf("1048575 bytes should be '1024.0 KB', got %q", below)
		}
		if at != "1.0 MB" {
			t.Errorf("1048576 bytes should be '1.0 MB', got %q", at)
		}
	})

	t.Run("GB boundary", func(t *testing.T) {
		below := formatSize(1073741823)
		at := formatSize(1073741824)

		if below != "1024.0 MB" {
			t.Errorf("1073741823 bytes should be '1024.0 MB', got %q", below)
		}
		if at != "1.0 GB" {
			t.Errorf("1073741824 bytes should be '1.0 GB', got %q", at)
		}
	})
}

// TestFormatSizeRealistic tests realistic file sizes
func TestFormatSizeRealistic(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{"small text file", 1500, "1.5 KB"},
		{"word document", 52428, "51.2 KB"},
		{"photo", 3145728, "3.0 MB"},
		{"video clip", 157286400, "150.0 MB"},
		{"movie file", 4718592000, "4.4 GB"},
		{"backup archive", 53687091200, "50.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}
