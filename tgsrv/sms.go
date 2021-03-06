package tgsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const phonePattern = `^\+?[0-9]+$`

var phoneRE = regexp.MustCompile(phonePattern)

type SMSClient struct {
	IfTTTKey string
}

func NewSMSClient(iftttKey string) *SMSClient {
	return &SMSClient{IfTTTKey: iftttKey}
}

// curl -X POST -H "Content-Type: application/json" -d '{"value1":"89990010203","value2":"привет как дела7"}' https://maker.ifttt.com/trigger/sendsms/with/key/xxxxxxxxxxx
func (b *TGBot) sms(phone string, sms string) {
	s := SMS{Phone: phone, Msg: sms, CreatedAt: time.Now().In(Location).UnixMilli()}
	err := b.smses.Insert(s)
	if err != nil {
		Logger.Errorf("error inserting sms: phone=%s, sms=%q  %v", phone, sms, err)
	}
}

func (b *TGBot) smsSenderLoop() {
	smsSenderLoopRates(b, b.SMSRateLimiter, make(chan struct{}))
}

func smsSenderLoopRates(b SMSSendingLoop, rates []Rate, done chan struct{}) {
	tickers := make([]*time.Ticker, 0, 5)
	limits := make([]int, 5)
	ratesC := make([]<-chan time.Time, 5)
	for i, r := range rates {
		t := time.NewTicker(r.Ticker)
		tickers = append(tickers, t)
		ratesC[i] = t.C
		limits[i] = r.Cnt
	}
	defer func() {
		for _, t := range tickers {
			t.Stop()
		}
		close(done)
	}()

	var smses []SMS
Loop:
	for {
		select {
		case <-ratesC[0]:
			// check limits
			for i := range rates {
				if i == 0 {
					continue
				}
				if limits[i] == 0 { // no limit
					continue Loop
				}
			}
			// select from db next n sms
			if len(smses) == 0 {
				var err error
				smses, err = b.smsesDAO().ListNew()
				if err != nil {
					Logger.Errorf("%v", err)
					continue
				}
				if len(smses) == 0 {
					continue
				}
			}
			sms := smses[0]
			smses = smses[1:]
			//Logger.Infof("SMS: %s %q", sms.Phone, sms.Msg)
			b.smsSender().sendSMS(sms.Phone, sms.Msg)
			sms.SentAt = time.Now().UnixMilli()
			err := b.smsesDAO().Update(sms)
			if err != nil {
				Logger.Errorf("%v", err)
			}
			for i, n := range limits {
				if n <= 0 || i == 0 {
					continue
				}
				limits[i] -= rates[0].Cnt
			}
		case <-ratesC[1]:
			limits[1] = rates[1].Cnt
		case <-ratesC[2]:
			limits[2] = rates[2].Cnt
		case <-ratesC[3]:
			limits[3] = rates[3].Cnt
		case <-ratesC[4]:
			limits[4] = rates[4].Cnt
		case <-b.abortChan():
			return
		}
	}
}

func (c *SMSClient) sendSMS(phone string, sms string) bool {
	if strings.HasPrefix(phone, "+7") {
		phone = "8" + phone[2:]
	}
	if strings.HasPrefix(phone, "849") {
		return false
	}
	values := map[string]interface{}{
		"value1": phone,
		"value2": sms,
	}
	body, err := json.Marshal(values)
	if err != nil {
		Logger.Errorf("error marshalling")
		return false
	}
	url := `https://maker.ifttt.com/trigger/sendsms/with/key/` + c.IfTTTKey
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		Logger.Errorf("error creating POST %v %q", err, body)
		return false
	}
	Logger.Infof("SMS: %s, %q", phone, sms)
	return c.doRequest(req, body)
}

func (c *SMSClient) doRequest(req *http.Request, body []byte) bool {
	req.Header.Set("Content-Type", "application/json")
	var respBody string
	err := c.doRequestFunc(req, bodyFunc(&respBody))
	if err != nil {
		Logger.Errorf("error doing POST %s %s %v", req.URL, string(body), err)
		return false
	}
	return true
}

func (c *SMSClient) doRequestFunc(req *http.Request, ff ...func(io.Reader) error) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return err
	}
	if len(ff) < 1 {
		for _, f := range ff {
			if err := f(resp.Body); err != nil {
				return err
			}
		}
		return nil
	}
	bb, err := ioutil.ReadAll(resp.Body)
	for _, f := range ff {
		if err := f(bytes.NewReader(bb)); err != nil {
			return err
		}
	}
	return nil
}
