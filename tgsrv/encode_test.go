package tgsrv

import (
	"testing"
)

func TestEncrypt(t *testing.T) {
	tests := []struct {
		text string
	}{
		{"79991230010203604140800"},
		{"79991230010203604140801"},
	}
	for _, tt := range tests {
		urlPart, err := Encrypt(tt.text)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		t.Log(urlPart)
		got, err := Decrypt(urlPart)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != tt.text {
			t.Errorf("Encrypt() got = %v, want %v", got, tt.text)
		}
	}
}
