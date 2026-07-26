package utils

import "testing"

func TestByte2FileSizePreservesFractionalUnits(t *testing.T) {
	if got, want := Byte2FileSize(1536), "1.50 KB"; got != want {
		t.Fatalf("Byte2FileSize(1536) = %q, want %q", got, want)
	}
}
