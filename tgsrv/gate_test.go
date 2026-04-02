package tgsrv

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestBTMacsFromTOML(t *testing.T) {
	tomlData := `
BLEAutoOpenLagMin = 3

[BTMacSystem]
"CA:7E:07:37:41:48" = "pal-es spider i-wr"
"CA:7E:07:37:41:4E" = "pal-es spider i-wr"
"CA:7E:07:37:41:4F" = "pal-es spider i-wr"
"5B:00:DF:94:DD:1C" = "79990010203"

[BTMacIgnore]
"E4:AE:E4:50:2A:D5" = "TUYA_ градусник"

[BTMacAutoOpenGate]
"5B:0B:2D:AC:B1:E7" = "79990010203"

[BTMacNames]
"0C:B7:89:14:26:AB" = "серебрисый"
"10:E9:53:FB:E9:AE" = "невидимка"
`

	var result BTMacs
	_, err := toml.Decode(tomlData, &result)
	if err != nil {
		t.Fatalf("Ошибка парсинга TOML: %v", err)
	}
	{
		got := result.BTMacIgnore["E4:AE:E4:50:2A:D5"]
		want := "TUYA_ градусник"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := result.BTMacAutoOpenGate["5B:0B:2D:AC:B1:E7"]
		want := "79990010203"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestPalEsTimeGroupsFromJson(t *testing.T) {
	jsonData := `{
    "err": null,
    "errId": 0,
    "msg": "success",
    "groups": {
        "count": 1,
        "start": 0,
        "len": 1,
        "list": [
            {
                "_id": "19292",
                "createdAt": "2026-03-31T05:52:41.993Z",
                "orgId": "15479",
                "deviceId": "4G600211776",
                "groupName": "shop",
                "timeArray": [
                    {
                        "s": 540,
                        "e": 1200,
                        "d": 1
                    },
                    {
                        "s": 0,
                        "e": 1375,
                        "d": 2
                    }
                ],
                "startTime": 0,
                "endTime": 1440,
                "days": "1234567",
                "startDate": 1777582800,
                "endDate": 1792097998,
                "groupUuid": "19292",
                "crc32": 1330448267
            }
        ]
    }
}
`
	var tg PalEsTimeGroups
	err := json.Unmarshal([]byte(jsonData), &tg)
	if err != nil {
		t.Fatalf("Ошибка парсинга: %v", err)
	}
	tg.init()
	if tg.Groups.List[0].Days != "1234567" {
		t.Errorf("got = %v, want %v", tg.Groups.List[0].Days, "1234567")
	}
	if tg.Groups.List[0].GroupName != "shop" {
		t.Errorf("got = %v, want %v", tg.Groups.List[0].GroupName, "shop")
	}
	if tg.Groups.List[0].StartDate != 1777582800 {
		t.Errorf("got = %v, want %v", tg.Groups.List[0].StartDate, 1777582800)
	}
	if len(tg.Groups.List[0].TimeArray) != 2 {
		t.Errorf("got = %v, want %v", len(tg.Groups.List[0].TimeArray), 2)
	}
	loc, _ := time.LoadLocation("Europe/Moscow")
	type test struct {
		groupId   string
		groupName string
		date      time.Time
		want      bool
	}
	tests := []test{
		{"", "shop", time.Date(2026, time.March, 31, 0, 0, 0, 0, loc), false},
		{"", "shop", time.Date(2026, time.June, 1, 0, 0, 0, 0, loc), true},
		{"", "shop", time.Date(2026, time.June, 1, 12, 0, 0, 0, loc), true},
		{"", "shop", time.Date(2026, time.June, 7, 0, 0, 0, 0, loc), false},
		{"", "shop", time.Date(2026, time.June, 7, 9, 0, 0, 0, loc), true},
		{"", "shop", time.Date(2026, time.June, 7, 8, 59, 0, 0, loc), false},
		{"", "shop", time.Date(2026, time.June, 7, 20, 0, 0, 0, loc), true},
		{"", "shop", time.Date(2026, time.June, 7, 20, 1, 0, 0, loc), false},
		{"", "shop", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), false},

		{"19292", "", time.Date(2026, time.March, 31, 0, 0, 0, 0, loc), false},
		{"19292", "", time.Date(2026, time.June, 1, 0, 0, 0, 0, loc), true},
		{"19292", "", time.Date(2026, time.June, 1, 12, 0, 0, 0, loc), true},
		{"19292", "", time.Date(2026, time.June, 7, 0, 0, 0, 0, loc), false},
		{"19292", "", time.Date(2026, time.June, 7, 9, 0, 0, 0, loc), true},
		{"19292", "", time.Date(2026, time.June, 7, 8, 59, 0, 0, loc), false},
		{"19292", "", time.Date(2026, time.June, 7, 20, 0, 0, 0, loc), true},
		{"19292", "", time.Date(2026, time.June, 7, 20, 1, 0, 0, loc), false},
		{"19292", "", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), false},

		{"", "xxx", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), true},
		{"", "", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), true},
	}
	for _, tt := range tests {
		got := tg.contains(tt.groupId, tt.groupName, tt.date)
		if tt.want != got {
			t.Errorf("%s  got %v, want %v", tt.date.Format("2006-01-02 15:04:05"), got, tt.want)
		}
	}
	got := tg.Groups.List[0].Id
	want := "19292"
	if want != got {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPalEsEmptyTimeGroups(t *testing.T) {
	var tg PalEsTimeGroups
	tg.init()
	loc, _ := time.LoadLocation("Europe/Moscow")
	type test struct {
		groupId   string
		groupName string
		date      time.Time
		want      bool
	}
	tests := []test{
		{"", "shop", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), true},
		{"", "xxx", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), true},
		{"", "", time.Date(2026, time.November, 1, 12, 0, 0, 0, loc), true},
	}
	for _, tt := range tests {
		got := tg.contains(tt.groupId, tt.groupName, tt.date)
		if tt.want != got {
			t.Errorf("%s  got %v, want %v", tt.date.Format("2006-01-02 15:04:05"), got, tt.want)
		}
	}
}
