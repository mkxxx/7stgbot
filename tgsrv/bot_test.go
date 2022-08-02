package tgsrv

import (
	"testing"
)

func TestPhoneRE(t *testing.T) {
	type test struct {
		phone string
		want  bool
	}
	tests := []test{
		{"89990010203", true},
		{"+79990010203", true},
		{"+7999 0010203", false},
		{"8(999)0010203", false},
	}
	for _, tt := range tests {
		got := phoneRE.MatchString(tt.phone)
		if tt.want != got {
			t.Errorf("%s  got %v, want %v", tt.phone, got, tt.want)
		}
	}
}
