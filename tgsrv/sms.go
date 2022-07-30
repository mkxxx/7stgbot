package tgsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
)

type SMSClient struct {
	IfTTTKey string
}

func NewSMSClient(iftttKey string) *SMSClient {
	return &SMSClient{IfTTTKey: iftttKey}
}

// curl -X POST -H "Content-Type: application/json" -d '{"value1":"89990010203","value2":"привет как дела7"}' https://maker.ifttt.com/trigger/sendsms/with/key/xxxxxxxxxxx
func (c *SMSClient) sms(phone string, sms string) bool {
	if strings.HasPrefix(phone, "8") {
		phone = "+7" + phone[1:]
	}
	if strings.HasPrefix(phone, "+749") {
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
