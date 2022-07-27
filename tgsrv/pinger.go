package tgsrv

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PublicIpNet = "91.234.180."
	//	PublicIp    = "91.234.180.53"
)

func StartPinger(abort chan struct{}, discordAlertChannelURL string) *pingMonitor {
	p := newPingMonitor(discordAlertChannelURL)
	go p.run(abort)
	go p.watchOnlineCnt()
	return p
}

func newPingMonitor(discordAlertChannelURL string) *pingMonitor {
	offline := make(map[string]bool)
	for i := 2; i < 255; i++ {
		offline[PublicIpNet+strconv.Itoa(i)] = true
	}
	return &pingMonitor{
		online:                 make(map[string]pingTime),
		offline:                offline,
		onlineChanged:          make(chan IntUpdate, 1),
		discordAlertChannelURL: discordAlertChannelURL,
	}
}

type IntUpdate struct {
	old int
	new int
}

type pingMonitor struct {
	mu                     sync.Mutex
	online                 map[string]pingTime
	offline                map[string]bool
	onlineCnt              int
	onlineChanged          chan IntUpdate
	discordAlertChannelURL string
}

type pingTime struct {
	t  time.Time
	ok bool
}

func (m *pingMonitor) run(abort chan struct{}) {
	//Loop:
	t := time.Now()
	for {
		for i := 2; i < 255; i++ {
			/*
				select {
				case _, ok := <-abort:
					if !ok {
						break Loop
					}
				default:
				}

			*/
			now := time.Now()
			// each minute check if some online ip went offline and recheck all online ips
			// to report about network outage early
			if now.Sub(t) >= time.Minute {
				t = now
				ok := m.ping(m.bestIP(1))
				if !ok {
					ips := m.IPs(true)
					for _, ip := range ips {
						m.ping(ip)
					}
				}
			}
			addr := PublicIpNet + strconv.Itoa(i)
			m.ping(addr)
		}
	}
}

func (m *pingMonitor) ping(addr string) bool {
	var (
		prevOnlineCnt = 0
		onlineCnt     = -1
	)
	out, err := exec.Command("ping", addr, "-c", "3", "-w", "5").CombinedOutput()
	ok := !strings.Contains(string(out), "100% packet loss")
	if err != nil {
		if !ok {
			m.mu.Lock()
			prevOnlineCnt = m.onlineCnt
			if !m.offline[addr] {
				wasOK := m.online[addr].ok
				m.online[addr] = pingTime{time.Now(), false}
				if wasOK {
					m.onlineCnt--
				}
			}
			onlineCnt = m.onlineCnt
			m.mu.Unlock()
			return true
		}
		// Logger.Errorf("ping -c 3 -w 5 %s: %v", addr, err)
		time.Sleep(time.Second)
		return true
	}
	m.mu.Lock()
	prevOnlineCnt = m.onlineCnt
	if ok {
		delete(m.offline, addr)
		wasOK := m.online[addr].ok
		m.online[addr] = pingTime{time.Now(), true}
		if !wasOK {
			m.onlineCnt++
		}
	} else if !m.offline[addr] {
		wasOK := m.online[addr].ok
		m.online[addr] = pingTime{time.Now(), false}
		if wasOK {
			m.onlineCnt--
		}
	}
	onlineCnt = m.onlineCnt
	m.mu.Unlock()
	if onlineCnt != -1 && onlineCnt != prevOnlineCnt {
		m.onlineChanged <- IntUpdate{prevOnlineCnt, onlineCnt}
	}
	/*			pinger, err := ping.NewPinger(addr)
				if err != nil {
					Logger.Errorf("could not create pinger %s", addr)
					time.Sleep(time.Millisecond * 500)
					continue
				}
				pinger.OnRecv = func(pkt *ping.Packet) {
					pinger.Stop()
				}
				done := make(chan struct{})
				pinger.OnFinish = func(stats *ping.Statistics) {
					addr := stats.IPAddr.String()
					m.mu.Lock()
					if stats.PacketsRecv > 0 {
						delete(m.offline, addr)
						m.online[addr] = pingTime{time.Now(), true}
					} else if !m.offline[addr] {
						m.online[addr] = pingTime{time.Now(), false}
					}
					m.mu.Unlock()
					close(done)
				}
				err = pinger.Run()
				if err != nil {
					Logger.Errorf("ping -c 3 -w 5 %s: %v", addr, err)
					time.Sleep(time.Second)
					continue
				}
				timer := time.NewTimer(time.Second * 2)
				select {
				case <-done:
				//case <-abort:
				case <-timer.C:
					pinger.Stop()
				}*/
	return ok
}

func (m *pingMonitor) onlineCount() (onlineRecently int, reached int) {
	m.mu.Lock()
	reached = len(m.online)
	for _, v := range m.online {
		if v.ok {
			onlineRecently++
		}
	}
	m.mu.Unlock()
	return onlineRecently, reached
}

type ipAge struct {
	age time.Duration
	ip  string
}

type byAge []ipAge

func (a byAge) Len() int      { return len(a) }
func (a byAge) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byAge) Less(i, j int) bool {
	if a[i].age != a[j].age {
		return a[i].age < a[j].age
	}
	return a[i].ip < a[j].ip
}

func (m *pingMonitor) bestIP(randomize int) string {
	if randomize <= 0 {
		randomize = 1
	}
	ips := make([]ipAge, 0, len(m.online))
	now := time.Now()
	m.mu.Lock()
	for k, v := range m.online {
		if !v.ok {
			continue
		}
		age := now.Sub(v.t)
		ips = append(ips, ipAge{age, k})
	}
	m.mu.Unlock()
	if len(ips) == 0 {
		return PublicIpNet + "1"
	}
	sort.Sort(byAge(ips))
	return ips[rand.Intn(len(ips))].ip
}

func (m *pingMonitor) IPs(online bool) []string {
	var ips = make([]string, 0, len(m.online))
	m.mu.Lock()
	for k, v := range m.online {
		if !online || v.ok {
			ips = append(ips, k)
		}
	}
	m.mu.Unlock()
	return ips
}

func (m *pingMonitor) watchOnlineCnt() {
	var triggeredTime time.Time
	for cnt := range m.onlineChanged {
		if cnt.new >= cnt.old {
			continue
		}
		if cnt.new != 1 || time.Since(triggeredTime) < 24*time.Hour {
			continue
		}
		triggeredTime = time.Now()
		reportToDiscord(m.discordAlertChannelURL, "semislavka.win: possibly network outage")
	}
}

func reportToDiscord(url, msg string) {
	client := http.Client{
		Timeout: 5 * time.Second,
	}
	jsonMsg, err := discordFormat(msg)
	if err != nil {
		Logger.Errorf("message format error '%v' for %q", err, msg)
		return
	}
	if len(url) != 0 {
		resp, err := client.Post(url, "application/json", strings.NewReader(jsonMsg))
		if err != nil {
			Logger.Errorf("post %s error %v", url, err)
		} else {
			resp.Body.Close()
		}
	} else {
		Logger.Warn(msg)
	}
}

func discordFormat(msg string) (string, error) {
	m := map[string]string{
		"content": msg,
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(m)
	return buf.String(), err
}
