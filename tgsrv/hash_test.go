package tgsrv

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeEmailAndHash(t *testing.T) {
	tests := []struct {
		email string
		want  string
	}{
		{"abcdef+preds@gmail.com",
			"abcdef@gmail.com"},
		{"a.b.c.d.e.f.+p.r.e.d.s.@gmail.com",
			"a.b.c.d.e.f.@gmail.com"},
		{"a@ya.ru",
			"a@ya.ru"},
		{"abcdefghijklmnopqrstuvwxyz+abcdefghijklmnopqrstuvwxyz@gmail.com",
			"abcdefghijklmnopqrstuvwxyz@gmail.com"},
		{"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz@gmail.com",
			"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz@gmail.com"},
	}
	for _, tt := range tests {
		key := encodeEmailAndMD5(tt.email)
		fmt.Println(key)
		got, err, ok := decodeEmailAndMD5(key)
		if !strings.HasPrefix(tt.want, got) {
			t.Errorf("decodeEmailAndHash() got = %v, want %v", got, tt.want)
		}
		if err != nil {
			t.Error(tt.email, err)
		} else if !ok {
			t.Errorf("%s decodeEmailAndHash() got = %v, want %v", tt.email, ok, true)
		}
	}
}
