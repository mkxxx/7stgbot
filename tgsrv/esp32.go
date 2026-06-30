package tgsrv

import (
	"encoding/hex"
	"errors"
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
}

func (t *BLETracking) init() error {
	if t.Raw == "00" {
		return nil
	}
	bytes, err := hex.DecodeString(t.Raw)
	if err != nil {
		return err
	}
	var structures []ADStructure
	cursor := 0
	length := len(bytes)
	for cursor < length {
		// Читаем длину текущей AD-структуры
		adLength := bytes[cursor]
		if adLength == 0 {
			// Нулевая длина означает конец полезной нагрузки или паддинг
			break
		}
		// Проверяем, не выходит ли структура за границы массива
		if cursor+1+int(adLength) > length {
			return errors.New("malformed BLE payload: length field exceeds remaining bytes")
		}
		// Читаем тип данных
		adType := bytes[cursor+1]
		// Извлекаем сами данные структуры
		adData := bytes[cursor+2 : cursor+1+int(adLength)]
		structures = append(structures, ADStructure{
			Length: adLength,
			Type:   adType,
			Data:   adData,
		})
		// Сдвигаем курсор на следующую структуру (1 байт длины + её размер)
		cursor += 1 + int(adLength)
	}
	var sb strings.Builder
	for _, s := range structures {
		s.StringBld(sb)
	}
	return nil
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
	if t.Raw != "" {
		sb.WriteString(" Raw: ")
		sb.WriteString(t.Raw)
	}
	return sb.String()
}

// ADStructure представляет одну структуру данных внутри BLE пакета
type ADStructure struct {
	Length uint8
	Type   uint8
	Data   []byte
}

func (ad *ADStructure) StringBld(sb strings.Builder) {
	switch ad.Type {
	case DataTypeLocalNameFull, DataTypeLocalNameShort:
		sb.WriteString(` "`)
		sb.WriteString(string(ad.Data))
		sb.WriteString(`"`)

	case DataTypeFlags:
		if len(ad.Data) > 0 {
			sb.WriteString(fmt.Sprintf(" Flags: 0x%02X", ad.Data[0]))
		}

	case DataTypeTxPower:
		if len(ad.Data) > 0 {
			sb.WriteString(" [Tx Power]: ")
			sb.WriteString(strconv.Itoa(int(ad.Data[0])))
			sb.WriteString(" dBm")
		}

	case DataTypeManufacturer:
		if len(ad.Data) >= 2 {
			// Первые два байта данных изготовителя — это Company ID (Little Endian)
			companyID := uint16(ad.Data[0]) | uint16(ad.Data[1])<<8
			companyData := ad.Data[2:]
			fmt.Printf("   [Данные изготовителя (0xFF)]: Company ID: 0x%04X, Payload: %X\n", companyID, companyData)

			// Пример: Разбор iBeacon от Apple
			if companyID == 0x004C && len(companyData) >= 21 && companyData[0] == 0x02 {
				uuid := hex.EncodeToString(companyData[2:18])
				major := uint16(companyData[18])<<8 | uint16(companyData[19])
				minor := uint16(companyData[20])<<8 | uint16(companyData[21])
				fmt.Printf("      -> Обнаружен iBeacon! UUID: %s, Major: %d, Minor: %d\n", uuid, major, minor)
			}
		}

	case DataTypeServiceData16:
		if len(ad.Data) >= 2 {
			// Первые два байта — 16-битный UUID Сервиса (Little Endian)
			serviceUUID := uint16(ad.Data[0]) | uint16(ad.Data[1])<<8
			servicePayload := ad.Data[2:]
			fmt.Printf("   [Данные Сервиса 16-бит (0x16)]: UUID Сервиса: 0x%04X, Данные: %X\n", serviceUUID, servicePayload)
		}

	default:
		fmt.Printf("   [Тип 0x%02X]: Данные (Len %d): %X\n", ad.Type, ad.Length-1, ad.Data)
	}

}
