package tgsrv

import (
	"testing"
)

func TestValueDatesCoef(t *testing.T) {
	var vdvd valueDates
	m := map[string]float64{"202206": 10.2, "202201": 8.7}
	vdvd.fromMap(m)

	type test struct {
		year  int
		month int
		want  float64
	}
	tests := []test{
		{2021, 12, 8.7},
		{2022, 2, 8.7},
		{2022, 5, 8.7},
		{2022, 5, 8.7},
		{2022, 6, 10.2},
		{2023, 6, 10.2},
	}
	for _, tt := range tests {
		got := vdvd.coef(tt.year, tt.month)
		if tt.want != got {
			t.Errorf("coef() = %v, want %v", got, tt.want)
		}
	}
}
