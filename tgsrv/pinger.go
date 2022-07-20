package tgsrv

import (
	"github.com/go-ping/ping"
	"strconv"
	"sync"
	"time"
)

const (
	PublicIpNet = "91.234.180."
	//	PublicIp    = "91.234.180.53"
)

func StartPinger(abort chan struct{}) *pingMonitor {
	p := newPingMonitor()
	go p.run(abort)
	return p
}

func newPingMonitor() *pingMonitor {
	offline := make(map[string]bool)
	for i := 2; i < 255; i++ {
		offline[PublicIpNet+strconv.Itoa(i)] = true
	}
	return &pingMonitor{
		online:  make(map[string]pingTime),
		offline: offline,
	}
}

type pingMonitor struct {
	mu      sync.Mutex
	online  map[string]pingTime
	offline map[string]bool
}

type pingTime struct {
	t   time.Time
	ok  bool
	rtt time.Duration
}

func (m *pingMonitor) run(abort chan struct{}) {
Loop:
	for {
		for i := 2; i < 255; i++ {
			select {
			case <-abort:
				break Loop
			default:
			}
			addr := PublicIpNet + strconv.Itoa(i)
			pinger, err := ping.NewPinger(addr)
			if err != nil {
				Logger.Errorf("could not create pinger %s", addr)
				continue
			}
			go func() {
				timer := time.NewTimer(time.Second * 2)
				<-timer.C
				pinger.Stop()
			}()
			pinger.OnRecv = func(pkt *ping.Packet) {
				pinger.Stop()
			}
			done := make(chan struct{})
			pinger.OnFinish = func(stats *ping.Statistics) {
				addr := stats.IPAddr.String()
				m.mu.Lock()
				if stats.PacketsRecv > 0 {
					delete(m.offline, addr)
					m.online[addr] = pingTime{time.Now(), true, stats.AvgRtt}
				} else if !m.offline[addr] {
					m.online[addr] = pingTime{time.Now(), false, 0}
				}
				m.mu.Unlock()
				close(done)
			}
			err = pinger.Run()
			<-done
		}
	}
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

func (m *pingMonitor) bestIP() string {
	m.mu.Lock()
	addr := ""
	now := time.Now()
	minAge := time.Hour
	for k, v := range m.online {
		age := now.Sub(v.t)
		if v.ok && age < minAge {
			minAge = age
			addr = k
		}
	}
	m.mu.Unlock()
	if len(addr) != 0 {
		return addr
	}
	return PublicIpNet + "1"
}
