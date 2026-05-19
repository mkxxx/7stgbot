package tgsrv

import (
	"testing"
	"time"
)

func TestParseSyslog(t *testing.T) {
	type test struct {
		log  string
		want NetworkClientInfo
	}
	tests := []test{
		{`May 18 19:59:01 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".`,
			NetworkClientInfo{time.Date(2026, 5, 18, 19, 59, 01, 0, Location),
				"May 18 19:59:01", "10.1.30.64", "22:7c:b6:54:7d:27", "Mihail-s-Galaxy-Note10"}},
		{`May 18 20:03:05 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".`,
			NetworkClientInfo{time.Date(2026, 5, 18, 20, 03, 05, 0, Location),
				"May 18 20:03:05", "10.1.30.64", "22:7c:b6:54:7d:27", "Mihail-s-Galaxy-Note10"}},
		{`May 18 20:05:06 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".`,
			NetworkClientInfo{time.Date(2026, 5, 18, 20, 05, 06, 0, Location),
				"May 18 20:05:06", "10.1.30.64", "22:7c:b6:54:7d:27", "Mihail-s-Galaxy-Note10"}},
		{`May 18 20:01:52 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 192.168.1.57 from 5a:2c:ce:48:f3:60 hostname "Mihail-s-Galaxy-Note10".`,
			NetworkClientInfo{time.Date(2026, 5, 18, 20, 01, 52, 0, Location),
				"May 18 20:01:52", "192.168.1.57", "5a:2c:ce:48:f3:60", "Mihail-s-Galaxy-Note10"}},
	}
	for i, tt := range tests {
		got, ok := parseDHCPLog(tt.log)
		if !ok {
			t.Errorf("want ok, got !ok")
		}
		if tt.want != got {
			t.Errorf("%d: got %v, want %v", i, got, tt.want)
		}
	}
}
