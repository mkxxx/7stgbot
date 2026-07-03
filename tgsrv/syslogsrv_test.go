package tgsrv

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func init() {
	Logger = zap.NewNop().Sugar()
}

// May 22 13:13:22 Netcraze-7708 ndhcps: DHCPDISCOVER received from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".  (за < 2 сек до)
// вход (и ip):
// May 22 13:13:22 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10_".  (за < 1 сек до)
// вход (и ip):
// May 22 13:13:23 Netcraze-7708 ndhcps: sending ACK of 10.1.30.64 to 22:7c:b6:54:7d:27.
// выход:
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had been aged-out and disassociated (idle silence).
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had disassociated by STA (reason: STA is leaving or has left BSS).
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(2e:7e:3e:8a:25:a3) had deauthenticated by STA (reason: STA is leaving or has left BSS).

func TestParseSyslog(t *testing.T) {
	type test struct {
		syslog   string
		want     *NetworkClientInfo
		hostname string
	}
	tests := []test{
		{`May 22 13:13:22 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10_".`,
			nil,
			"Mihail-s-Galaxy-Note10_"},
		{`May 22 13:13:22 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".`,
			nil,
			"Mihail-s-Galaxy-Note10"},
		{`May 22 13:13:23 Netcraze-7708 ndhcps: sending ACK of 10.1.30.64 to 22:7c:b6:54:7d:27.`,
			&NetworkClientInfo{time.Date(2026, 5, 22, 13, 13, 23, 0, Location), "22:7c:b6:54:7d:27", "10.1.30.64",
				"Mihail-s-Galaxy-Note10", true},
			""},
		{`May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had been aged-out and disassociated (idle silence).`,
			&NetworkClientInfo{time.Date(2026, 5, 22, 13, 23, 22, 0, Location), "22:7c:b6:54:7d:27", "",
				"", false},
			""},
		{`May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had disassociated by STA (reason: STA is leaving or has left BSS).`,
			&NetworkClientInfo{time.Date(2026, 5, 22, 13, 23, 22, 0, Location), "22:7c:b6:54:7d:27", "",
				"", false},
			""},
		{`May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(2e:7e:3e:8a:25:a3) had deauthenticated by STA (reason: STA is leaving or has left BSS).`,
			&NetworkClientInfo{time.Date(2026, 5, 22, 13, 23, 22, 0, Location), "2e:7e:3e:8a:25:a3", "",
				"", false},
			""},
	}
	now := time.Date(2026, 5, 22, 13, 13, 30, 0, Location)
	hostnames := make(map[string]*NetworkClientInfo)
	var hostnamesTime time.Time
	for i, tt := range tests {
		got := parseDHCPLog(tt.syslog, now, hostnames, &hostnamesTime)
		if (tt.want == nil) != (got == nil) {
			t.Errorf("%d: got %v, want %v", i, got, tt.want)
		} else if got != nil && *tt.want != *got {
			t.Errorf("%d: got %v, want %v", i, got, tt.want)
		}
		if got == nil {
			nci := hostnames["22:7c:b6:54:7d:27"]
			got := ""
			if nci != nil {
				got = nci.Hostname
			}
			want := tt.hostname
			if got != want {
				t.Errorf("%d: got %v, want %v", i, got, want)
			}
		}
	}
}
