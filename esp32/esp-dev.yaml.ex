esp32_ble_server:
  manufacturer: "ESPHome"
  model: "ESP32 BLE Terminal (IDF)"
  on_connect:
    - logger.log: "BLE client connected"
  on_disconnect:
    - logger.log: "BLE client disconnected"
  services:
    - uuid: "4fafc201-1fb5-459e-8fcc-c5c9c331914b"
      advertise: true
      characteristics:
        - uuid: "beb5483e-36e1-4688-b7f5-ea07361b26ab"
          read: true
          write: true
          notify: true
          write_no_response: false
          value: "ESP32 BLE Terminal Ready (IDF)"
          on_write:
            - lambda: |-
                // x is already a std::vector<uint8_t> containing the written value
                std::vector<uint8_t> data = x;
                std::string received(data.begin(), data.end());
                ESP_LOGI("BLE", "Received command: %s", received.c_str());
                
                if (received == "ON") {
                  id(led).turn_on();
                } else if (received == "OFF") {
                  id(led).turn_off();
                }
                
                id(terminal_char).publish_state(received.c_str());

output:
  - platform: gpio
    id: led_output
    pin: GPIO4    # Changed from GPIO2 to a safer pin

light:
  - platform: binary
    name: "ESP32 BLE LED"
    id: led
    output: led_output

text_sensor:
  - platform: template
    id: terminal_char
    name: "Last BLE Command"

esp32_ble_tracker:
  on_ble_advertise:
    - then: 
        - lambda: |-
            ESP_LOGD("BLE", "Device: %s, RSSI: %d, Name: %s",
                     x.address_str().c_str(), x.get_rssi(), x.get_name().c_str());

            // Manufacturer data (может содержать iBeacon или Eddystone)
            auto manuf_datas = x.get_manufacturer_datas();
            if (!manuf_datas.empty()) {
              ESP_LOGD("BLE", "Manufacturer data count: %d", manuf_datas.size());
              for (auto &data : manuf_datas) {
                // Вывод company ID (первые два байта)
                if (data.data.size() >= 2) {
                  ESP_LOGD("BLE", "  Company ID: 0x%02X%02X", data.data[1], data.data[0]);
                }
                // Вывод длины данных
                ESP_LOGD("BLE", "  Data length: %d", data.data.size());
              }
            } else {
              ESP_LOGD("BLE", "No manufacturer data");
            }

            // Service data
            auto service_datas = x.get_service_datas();
            ESP_LOGD("BLE", "Service data count: %d", service_datas.size());
            for (auto &svc : service_datas) {
              std::string uuid_str = svc.uuid.to_string();
              ESP_LOGD("BLE", "  Service UUID: %s, data length: %d", uuid_str.c_str(), svc.data.size());
            }

            // iBeacon
            auto ibeacon = x.get_ibeacon();
            if (ibeacon.has_value()) {
              ESP_LOGD("BLE", "iBeacon detected!");
            } else {
              ESP_LOGD("BLE", "No iBeacon data");
            }
