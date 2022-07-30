package tgsrv

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

var client *http.Client

func init() {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client = &http.Client{Transport: tr}
}

func search(search string) (*SearchResult, error) {
	url := `https://lk.preds.ru/reestr/getUsers`
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		Logger.Errorf("%s %v", req.URL, err)
		return nil, err
	}
	q := req.URL.Query()
	q.Add("page", "1")
	q.Add("limit", "10000")
	q.Add("search", search)
	req.URL.RawQuery = q.Encode()

	res := &SearchResult{}
	var respBody string
	err = doRequestFunc(req, bodyFunc(&respBody), decodeFunc(res))
	//log.Println(respBody)
	if err != nil {
		Logger.Errorf("%s %v", req.URL, err)
		return nil, err
	}
	Logger.Debugf("Loaded %d registry records from %s", len(res.Records), url)
	return res, nil
}

func bodyFunc(body *string) func(r io.Reader) error {
	return func(r io.Reader) error {
		bb, err := ioutil.ReadAll(r)
		*body = string(bb)
		return err
	}
}

func decodeFunc(jsonToVal interface{}) func(r io.Reader) error {
	if jsonToVal == nil {
		return nil
	}
	return func(r io.Reader) error {
		return json.NewDecoder(r).Decode(jsonToVal)
	}
}

func doRequestFunc(req *http.Request, ff ...func(io.Reader) error) error {
	//c := netrc.CredMap["svnhost.aftpro.com"]
	req.AddCookie(&http.Cookie{Name: "PHPSESSID", Value: "g1fp8ls87llt19bse2119sl0ro"})
	client.Timeout = time.Second * 5
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("query failed: %s  %s", resp.Status, req.URL)
	}
	if len(ff) <= 1 {
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
