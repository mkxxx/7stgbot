#pragma once
#include <string>

struct BleRawDevice {
  std::string mac;
  int rssi;
  std::string raw;
};

