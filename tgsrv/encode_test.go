package tgsrv

import (
	"testing"
	"time"
)

func TestEncrypt(t *testing.T) {
	tests := []struct {
		text string
		time time.Time
		want time.Time
	}{
		{"79990010203", time.Date(2026, 5, 1, 12, 34, 56, 0, Location), time.Date(2026, 5, 1, 12, 0, 0, 0, Location)},
		{"79990010203", time.Date(2036, 5, 1, 12, 34, 56, 0, Location), time.Date(2036, 5, 1, 12, 0, 0, 0, Location)},
	}
	for _, tt := range tests {
		urlPart, err := EncryptPhone(tt.text, tt.time)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		t.Log(urlPart)
		phone, tm, err := DecryptPhone(urlPart)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if phone != tt.text {
			t.Errorf("Encrypt() got = %q, want %q", phone, tt.text)
		}
		tm = tm.In(Location)
		if tm != tt.want {
			t.Errorf("Encrypt() got = %v, want %v", tm, tt.want)
		}
	}
}
