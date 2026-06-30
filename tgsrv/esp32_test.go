package tgsrv

import (
	"testing"
)

func TestDecodeBLE(t *testing.T) {
	type test struct {
		raw string
		//want    string
	}
	tests := []test{ // единица добавляется в старший разряд
		{"02011a1bff7500021841b1b36aeff6f510258868214e52da79df6a401a61d508ff75002784171461"},
		{"02011a1bff4c000c0e08e56dcfda1128bacf2b7734decf10064d1d49b8c828"},
		{"0303f3fe1e16f3fe4a172345355a481132abeee9e49c65a2addb9e94ebd00e62c241b6"},
		{"07ff4c0012020002"},
	}
	for _, tt := range tests {
		bd, err := ParseRawBLE(tt.raw)
		if err != nil {
			t.Error(err)
		}
		//t.Errorf("%s", bd.String())
		if bd.LocalName != "" {

		}
		/*
			if got != tt.want {
				t.Errorf("%q: got %v, want %v", tt.badKeys, got, tt.want)
			}
		*/
	}
}
