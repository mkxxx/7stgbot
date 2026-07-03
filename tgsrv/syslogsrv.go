package tgsrv

import (
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	UDPIp   = "0.0.0.0"
	UDPPort = "1514"
)

var (
	hostNameRE = regexp.MustCompile(`(?i)from\s+(?P<mac>[0-9a-f]{2}(?::[0-9a-f]{2}){5})\s+hostname\s+"(?P<hostname>[^"]+)"`)
	ackRE      = regexp.MustCompile(`(?i)of\s+(?P<ip>\d{1,3}(?:\.\d{1,3}){3})\s+to\s+(?P<mac>[0-9a-f]{2}(?::[0-9a-f]{2}){5})`)
	macRE      = regexp.MustCompile(`(?i)STA\((?P<mac>[0-9a-f]{2}(?::[0-9a-f]{2}){5})\)`)
)

func (g *Gate) startSyslogListener(abort <-chan struct{}) {
	address := net.JoinHostPort(UDPIp, UDPPort)
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		Logger.Errorf("ResolveUDPAddr %q error: %v", address, err)
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		Logger.Errorf("ListenUDP %q error: %v", address, err)
		return
	}
	defer conn.Close()

	go func() {
		<-abort
		conn.Close()
	}()
	hostnames := make(map[string]*NetworkClientInfo)
	var hostnamesTime time.Time
	syslogRegex := regexp.MustCompile(`^<.*?>`)
	buf := make([]byte, 4096)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			// Если соединение закрыто через Ctrl+C, выходим без ошибки
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			Logger.Errorf("ReadFromUDP %q error: %v", address, err)
			continue
		}
		rawLine := strings.TrimSpace(string(buf[:n]))
		// Очистка от Syslog заголовков <...>
		cleanLine := strings.TrimSpace(syslogRegex.ReplaceAllString(rawLine, ""))
		if cleanLine == "" {
			continue
		}
		nci := parseDHCPLog(cleanLine, time.Now(), hostnames, &hostnamesTime)
		if nci != nil {
			g.wifiClients <- nci
		}
		Logger.Debugf("syslog: from: %s %s", remoteAddr.IP.String(), cleanLine)
	}
}

type PALESLogInfo struct {
	Phone     string
	Firstname string
	Lastname  string
}

type NetworkClientInfo struct {
	Time      time.Time
	MAC       string
	IP        string
	Hostname  string
	connected bool
}

// имя:
// May 22 13:13:22 Netcraze-7708 ndhcps: DHCPDISCOVER received from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".  (за < 2 сек до)
// вход (и ip):
// May 22 13:13:22 Netcraze-7708 ndhcps: DHCPREQUEST received (STATE_INIT) for 10.1.30.64 from 22:7c:b6:54:7d:27 hostname "Mihail-s-Galaxy-Note10".  (за < 1 сек до)
// вход (и ip):
// May 22 13:13:23 Netcraze-7708 ndhcps: sending ACK of 10.1.30.64 to 22:7c:b6:54:7d:27.
// выход:
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had been aged-out and disassociated (idle silence).
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(22:7c:b6:54:7d:27) had disassociated by STA (reason: STA is leaving or has left BSS).
// May 22 13:23:22 Netcraze-7708 ndm: Network::Interface::Mtk::WifiMonitor: "WifiMaster0/AccessPoint1": STA(2e:7e:3e:8a:25:a3) had deauthenticated by STA (reason: STA is leaving or has left BSS).
func parseDHCPLog(logLine string, now time.Time, hostnames map[string]*NetworkClientInfo, hostnameTime *time.Time) *NetworkClientInfo {
	spaceInd := strings.Index(logLine, " ")
	if spaceInd < 0 || len(logLine) < spaceInd+11 {
		return nil
	}
	var t time.Time
	{
		logTimeStr := logLine[:spaceInd+12]
		t0, err := time.ParseInLocation("Jan 2 15:04:05", logTimeStr, Location)
		if err != nil {
			Logger.Errorf("syslog date parse error %s: %v", logTimeStr, err)
			t = now
		} else {
			t = t0.AddDate(now.Year(), 0, 0)
			if now.Sub(t) < -300*24*time.Hour {
				t = t.AddDate(-1, 0, 0)
			}
		}
	}
	if len(hostnames) != 0 && t.Sub(*hostnameTime) > 10*time.Second {
		clear(hostnames)
	}
	w := "DHCPDISCOVER"
	i := strings.Index(logLine, w)
	if i < 0 {
		w = "DHCPREQUEST"
		i = strings.Index(logLine, w)
	}
	if i >= 0 {
		match := hostNameRE.FindStringSubmatch(logLine[i+len(w)+1:])
		if match != nil {
			mac, hostname := "", ""
			for i, name := range hostNameRE.SubexpNames() {
				switch name {
				case "mac":
					mac = match[i]
				case "hostname":
					hostname = match[i]
				}
			}
			if mac != "" && hostname != "" {
				if len(hostnames) == 0 {
					*hostnameTime = t
				}
				hostnames[mac] = &NetworkClientInfo{Time: t, MAC: mac, Hostname: hostname}
			}
		}
		return nil
	}
	w = "ACK"
	i = strings.Index(logLine, "ACK of 10.")
	if i >= 0 {
		match := ackRE.FindStringSubmatch(logLine[i+len(w)+1:])
		if match != nil {
			mac, ip := "", ""
			for i, name := range ackRE.SubexpNames() {
				switch name {
				case "mac":
					mac = match[i]
				case "ip":
					ip = match[i]
				}

			}
			if mac != "" {
				hostname := ""
				if nci, ok := hostnames[mac]; ok {
					if t.Sub(nci.Time) < 10*time.Second {
						hostname = nci.Hostname
					}
				}
				return &NetworkClientInfo{Time: t, MAC: mac, IP: ip, Hostname: hostname, connected: true}
			}
		}
		return nil
	}
	if strings.Contains(logLine, "disassociated") || strings.Contains(logLine, "deauthenticated") {
		w = "WifiMaster0/AccessPoint1"
		i = strings.Index(logLine, w)
		if i >= 0 {
			match := macRE.FindStringSubmatch(logLine[i+len(w)+1+1:])
			if match != nil {
				for i, name := range macRE.SubexpNames() {
					if name == "mac" {
						return &NetworkClientInfo{Time: t, MAC: match[i], connected: false}
					}
				}
			}
			return nil
		}

	}
	return nil
}
