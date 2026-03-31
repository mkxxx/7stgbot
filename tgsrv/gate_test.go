package tgsrv

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestBTMacsFromTOML(t *testing.T) {
	// 1. Пример TOML-строки
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
