package tgsrv

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	UDPIp   = "0.0.0.0"
	UDPPort = "1514"
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
		if !strings.Contains(cleanLine, "DHCPREQUEST") {
			Logger.Debugf("syslog: from: %s %s", remoteAddr.IP.String(), cleanLine)
			continue
		}
		if !strings.Contains(cleanLine, "DHCPREQUEST received (STATE_INIT)") {
			continue
		}
		Logger.Infof("syslog: from: %s %s", remoteAddr.IP.String(), cleanLine)
		info, ok := parseDHCPLog(cleanLine)
		if !ok {
			continue
		}
		g.wifiClients <- &info
	}
}

type NetworkClientInfo struct {
	DateTime  time.Time
	Timestamp string
	IP        string
	MAC       string
	Hostname  string
}

func parseDHCPLog(logLine string) (NetworkClientInfo, bool) {
	// Регулярное выражение:
	// ^([A-Za-z]{3}\s+\d+\s+\d{2}:\d{2}:\d{2}) -> Группа 1: Дата и время в начале (например, May 18 19:59:01)
	// for\s+([\d\.]+)                           -> Группа 2: IP-адрес после слова for
	// from\s+([a-fA-F0-9:]{17})                -> Группа 3: MAC-адрес после слова from
	// hostname\s+"(.*?)"                       -> Группа 4: Имя устройства в кавычках
	re := regexp.MustCompile(`^([A-Za-z]{3}\s+\d+\s+\d{2}:\d{2}:\d{2}).*?for\s+([\d\.]+)\s+from\s+([a-fA-F0-9:]{17}).*?hostname\s+"(.*?)"`)

	matches := re.FindStringSubmatch(logLine)
	if len(matches) < 5 {
		return NetworkClientInfo{}, false
	}
	now := time.Now()
	dateStrWithYear := fmt.Sprintf("%d %s", now.Year(), matches[1])
	dateStrWithYear = strings.Join(strings.Fields(dateStrWithYear), " ")
	t, err := time.ParseInLocation("2006 Jan 2 15:04:05", dateStrWithYear, Location)
	if err != nil {
		Logger.Errorf("syslog date parse error %s: %v", dateStrWithYear, err)
		t = now
	} else {
		since := now.Sub(t)
		if since < -300*24*time.Hour {
			dateStrWithYear = fmt.Sprintf("%d %s", now.Year()-1, matches[1])
			dateStrWithYear = strings.Join(strings.Fields(dateStrWithYear), " ")
			t, err = time.ParseInLocation("2006 Jan 2 15:04:05", dateStrWithYear, Location)
			if err != nil {
				Logger.Errorf("syslog date parse error %s: %v", dateStrWithYear, err)
				t = now
			}
		}
	}
	return NetworkClientInfo{
		DateTime:  t,
		Timestamp: matches[1],
		IP:        matches[2],
		MAC:       matches[3],
		Hostname:  matches[4],
	}, true
}
