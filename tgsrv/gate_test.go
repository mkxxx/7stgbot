package tgsrv

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestBTMacsFromTOML(t *testing.T) {
	tomlData := `
BLEWatchLocation = 101

[BTMacIgnore]
"00:11:22:33:44:55" = "Test Device"

[BTMacAutoOpenGate]
"AA:BB:CC:DD:EE:FF" = true

[BTMacNames]
"11:22:33:44:55:66" = "Gate 1"
`

	var result BTMacs
	_, err := toml.Decode(tomlData, &result)
	if err != nil {
		t.Fatalf("Ошибка парсинга TOML: %v", err)
	}
	if result.BLEWatchLocation != 101 {
		t.Errorf("Ожидалось 101, получено %d", result.BLEWatchLocation)
	}
	if result.BTMacIgnore["00:11:22:33:44:55"] != "Test Device" {
		t.Error("BTMacIgnore: неверное значение")
	}
	if !result.BTMacAutoOpenGate["AA:BB:CC:DD:EE:FF"] {
		t.Error("BTMacAutoOpenGate: ожидалось true")
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
