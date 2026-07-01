package tgsrv

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Известные типы BLE Adv Data (Спецификация Bluetooth SIG)
const (
	DataTypeFlags          = 0x01 // Флаги устройства
	DataTypeServiceUUID16  = 0x03 // Полный список 16-бит UUID сервисов
	DataTypeLocalNameShort = 0x08 // Сокращенное имя устройства
	DataTypeLocalNameFull  = 0x09 // Полное имя устройства
	DataTypeTxPower        = 0x0A // Мощность передатчика
	DataTypeServiceData16  = 0x16 // Данные сервиса (16-бит UUID), например Xiaomi
	DataTypeManufacturer   = 0xFF // Данные производителя (Manufacturer Specific Data)
)

// {"mac":"5B:00:DF:94:DD:1C","rssi":-71,"location":2,"time":1775136766,    "uuid":"","name":"iTAG  ","company_id":56604,"raw":"..."}
type BLETracking struct {
	MAC       string
	RSSI      int
	Name      string
	UUID      string
	CompanyId int `json:"company_id"`
	Location  int
	Time      int64 // seconds
	Count     int
	Raw       string
	RawData   *BLEData
}

func (t *BLETracking) timestamp() string {
	return time.Unix(t.Time, 0).In(Location).Format("2006-01-02 15:04:05")
}

func (t *BLETracking) AsTime() time.Time {
	return time.Unix(t.Time, 0)
}

func (t *BLETracking) String() string {
	return t.StringNow(time.Time{})
}

func (t *BLETracking) StringNow(now time.Time) string {
	var sb strings.Builder
	sb.WriteString("BT-MAC ")
	sb.WriteString(t.MAC)
	if t.Name != "" {
		sb.WriteString(" \"")
		sb.WriteString(t.Name)
		sb.WriteString("\"")
	}
	if t.UUID != "" {
		sb.WriteString(" ")
		sb.WriteString(t.UUID)
	}
	if t.CompanyId != 0 {
		sb.WriteString(" Company: ")
		sb.WriteString(strconv.Itoa(t.CompanyId))
	}
	sb.WriteString(" RSSI: ")
	sb.WriteString(strconv.Itoa(t.RSSI))
	sb.WriteString(" Location: ")
	sb.WriteString(strconv.Itoa(t.Location))
	sb.WriteString(" N: ")
	sb.WriteString(strconv.Itoa(t.Count))
	if t.Time != 0 {
		sb.WriteString(" Time: ")
		sb.WriteString(t.timestamp())
		if !now.IsZero() {
			sb.WriteString(" (")
			sb.WriteString(now.Sub(t.AsTime()).Round(time.Millisecond).String())
			sb.WriteString(" ago)")
		}
	}
	t.ParseRaw()
	if t.RawData != nil {
		sb.WriteString(" Raw[")
		sb.WriteString(t.RawData.String())
		sb.WriteString("]")
	}
	return sb.String()
}

func (t *BLETracking) ParseRaw() {
	if t.Raw == "" || t.RawData != nil {
		return
	}
	bd, err := ParseRawBLE(t.Raw)
	if err != nil {
		Logger.Errorf("error parsing %q: %v", t.Raw, err)
	} else {
		t.RawData = bd
	}
}

type BLEFlags struct {
	LELimitedDiscoverable bool // Ограниченный режим обнаружения (быстро выключается)
	LEGeneralDiscoverable bool // Обычный режим обнаружения (виден всегда)
	BREDRNotSupported     bool // Чистый BLE-девайс (классический Bluetooth НЕ поддерживается)
	SimultaneousLE_BR     bool // Поддерживает одновременно BLE и классический BT (на стороне контроллера)
	SimultaneousLE_Host   bool // Поддерживает одновременно BLE и классический BT (на стороне хоста)
	All                   byte
}

func (f *BLEFlags) String() string {
	if f == nil {
		return ""
	}
	return hex.EncodeToString([]byte{f.All})
}

type BLEData struct {
	LocalName        string
	TxPower          int8
	ServiceUUIDs     []string
	ManufacturerID   uint16
	ManufacturerData []byte
	IBeacon          *IBeacon
	ServiceData      map[string][]byte
	Flags            *BLEFlags
}

func (bd *BLEData) String() string {
	sb := strings.Builder{}
	if bd.LocalName != "" {
		sb.WriteString(`"`)
		sb.WriteString(bd.LocalName)
		sb.WriteString(`"`)
	}
	if bd.TxPower != 0 {
		sb.WriteString(" pw: ")
		sb.WriteString(strconv.Itoa(int(bd.TxPower)))
	}
	if len(bd.ServiceUUIDs) != 0 {
		sb.WriteString(" UUID: ")
		sb.WriteString(strings.Join(bd.ServiceUUIDs, ""))
	}
	if bd.ManufacturerID != 0 {
		sb.WriteString(" Manufacturer: ")
		sb.WriteString(strconv.Itoa(int(bd.ManufacturerID)))
	}
	if bd.IBeacon != nil {
		sb.WriteString(" ")
		sb.WriteString(bd.IBeacon.String())
	} else if len(bd.ManufacturerData) != 0 {
		sb.WriteString(" Data: ")
		sb.WriteString(hex.EncodeToString(bd.ManufacturerData))
	}
	if bd.Flags != nil {
		sb.WriteString(" Flags: ")
		sb.WriteString(bd.Flags.String())
	}
	if len(bd.ServiceData) != 0 {
		sb.WriteString(" ServiceData: ")
		i := 0
		for k, v := range bd.ServiceData {
			if i != 0 {
				sb.WriteString(",")
			}
			sb.WriteString(k)
			sb.WriteString(":")
			sb.WriteString(hex.EncodeToString(v))
			i++
		}
		sb.WriteString(bd.Flags.String())
	}
	return sb.String()
}

// ParseRawBLE парсит сырой массив байт (adv_data + scan_rsp)
func ParseRawBLE(p string) (*BLEData, error) {
	payload, err := hex.DecodeString(p)
	if err != nil {
		return nil, err
	}
	var data BLEData
	data.ServiceData = make(map[string][]byte)

	offset := 0
	for offset < len(payload) {
		// 1. Читаем длину структуры. Если 0 — пакет закончился
		length := int(payload[offset])
		if length == 0 {
			break
		}
		// Защита от битых пакетов и выхода за границы слайса
		if offset+length+1 > len(payload) {
			break
		}
		// Извлекаем тип и сами данные структуры
		adType := payload[offset+1]
		adData := payload[offset+2 : offset+length+1]

		// Переходим к следующей GAP-структуре
		offset += length + 1
		switch adType {
		case DataTypeLocalNameShort, DataTypeLocalNameFull:
			data.LocalName = string(adData)

		case DataTypeTxPower:
			if len(adData) >= 1 {
				data.TxPower = int8(adData[0])
			}

		case 0x02, DataTypeServiceUUID16:
			for i := 0; i+2 <= len(adData); i += 2 {
				uuid := binary.LittleEndian.Uint16(adData[i : i+2])
				data.ServiceUUIDs = append(data.ServiceUUIDs, fmt.Sprintf("0x%04X", uuid))
			}

		case 0x06, 0x07: // 128-bit Service UUIDs
			for i := 0; i+16 <= len(adData); i += 16 {
				// Реверсируем байты, так как UUID в BLE передаются в Little-Endian
				revUUID := make([]byte, 16)
				for b := 0; b < 16; b++ {
					revUUID[b] = adData[i+15-b]
				}
				data.ServiceUUIDs = append(data.ServiceUUIDs, fmt.Sprintf("%x-%x-%x-%x-%x",
					revUUID[0:4], revUUID[4:6], revUUID[6:8], revUUID[8:10], revUUID[10:16]))
			}

		case DataTypeServiceData16:
			if len(adData) >= 2 {
				uuid := fmt.Sprintf("0x%04X", binary.LittleEndian.Uint16(adData[0:2]))
				data.ServiceData[uuid] = adData[2:]
			}

		case DataTypeManufacturer:
			if len(adData) >= 2 {
				// Первые 2 байта — ID компании (Apple, Microsoft, Xiaomi и т.д.)
				data.ManufacturerID = binary.LittleEndian.Uint16(adData[0:2])
				data.ManufacturerData = adData[2:]
			}

		case DataTypeFlags:
			if len(adData) >= 1 {
				flagsByte := adData[0]
				data.Flags = &BLEFlags{
					LELimitedDiscoverable: (flagsByte & 0x01) != 0, // Bit 0
					LEGeneralDiscoverable: (flagsByte & 0x02) != 0, // Bit 1
					BREDRNotSupported:     (flagsByte & 0x04) != 0, // Bit 2
					SimultaneousLE_BR:     (flagsByte & 0x08) != 0, // Bit 3
					SimultaneousLE_Host:   (flagsByte & 0x10) != 0, // Bit 4
					All:                   flagsByte,
				}
			}
		}
	}

	return &data, nil
}

// Разбор Apple iBeacon (Manufacturer ID: 0x004C)
func ParseIBeacon(manuData []byte) *IBeacon {
	if len(manuData) < 23 || manuData[0] != 0x02 || manuData[1] != 0x15 {
		return nil
	}
	ib := IBeacon{}
	// Извлекаем UUID, Major, Minor, TxPower
	ib.UUID = hex.EncodeToString(manuData[2:18])
	ib.Major = binary.BigEndian.Uint16(manuData[18:20]) // iBeacon использует BIG endian для Major/Minor!
	ib.Minor = binary.BigEndian.Uint16(manuData[20:22])
	ib.TxPower = int8(manuData[22])
	return &ib
}

type IBeacon struct {
	UUID    string
	Major   uint16
	Minor   uint16
	TxPower int8
}

func (b *IBeacon) String() string {
	return fmt.Sprintf("iBeacon UUID: %s Major: %d Minor: %d Power at 1m: %d", b.UUID, b.Major, b.Minor, b.TxPower)
}
