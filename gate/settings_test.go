package gate

import (
	"testing"
	"time"
)

func TestYAML(t *testing.T) {
	var sch Schedule
	{
		input := `{from: 22:00,to: 7:00,execTimeMilli: 0,execError: "error",execResult}`
		//input = `{from: "22:00",to: "7:00",execTimeMilli: 1782244800000,execError: some error}`
		err := UnmarshalYAMLOneLine(input, &sch)
		if err != nil {
			t.Error(err)
		}
	}
	loc := Location
	{
		now := time.Date(2026, time.June, 23, 12, 0, 0, 0, loc)
		{
			got := withTime(now, "")
			want := now
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			got := withTime(now, "9")
			want := time.Date(2026, time.June, 23, 9, 0, 0, 0, loc)
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			got := withTime(now, "9:05")
			want := time.Date(2026, time.June, 23, 9, 5, 0, 0, loc)
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			got := withTime(now, "9:05:11")
			want := time.Date(2026, time.June, 23, 9, 5, 11, 0, loc)
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			got := sch.IsTime(now)
			want := false
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	}
	{
		got := sch.IsTime(time.Date(2026, time.June, 23, 23, 0, 0, 0, loc))
		want := true
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := sch.IsTime(time.Date(2026, time.June, 23, 0, 0, 0, 0, loc))
		want := true
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		now := time.Date(2026, time.June, 23, 23, 0, 0, 0, loc)
		{
			sch.ExecTimeMilli = now.UnixMilli()
			got := sch.IsTime(now)
			want := false
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			sch.ExecTimeMilli = now.AddDate(0, 0, -1).UnixMilli()
			got := sch.IsTime(now)
			want := true
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	}
	{
		input := `{from: 22:00,to: 0:00}`
		UnmarshalYAMLOneLine(input, &sch)
		{
			got := sch.IsTime(time.Date(2026, time.June, 23, 0, 0, 0, 0, loc))
			want := false
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		{
			got := sch.IsTime(time.Date(2026, time.June, 23, 23, 0, 0, 0, loc))
			want := true
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	}
	{
		now := time.Date(2026, time.June, 23, 23, 0, 0, 0, loc)
		sch.ExecTimeMilli = now.UnixMilli()
		sch.ExecError = "some error"
		yamlStr := MarshalYAMLOneLine(&sch)
		var sch2 Schedule
		err := UnmarshalYAMLOneLine(yamlStr, &sch2)
		if err != nil {
			t.Error(err)
		}
		{
			got := sch2.From
			want := sch.From
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
		sch2.ExecResult = "done"
		yamlStr = MarshalYAMLOneLine(&sch2)
		var sch3 Schedule
		err = UnmarshalYAMLOneLine(yamlStr, &sch3)
		if err != nil {
			t.Error(err)
		}
		{
			got := sch3.From
			want := sch.From
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	}
}
