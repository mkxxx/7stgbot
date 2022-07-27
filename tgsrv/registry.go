package tgsrv

import (
	"log"
	"time"
)

var Location *time.Location

func init() {
	var err error
	Location, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Fatal(err)
	}
}

type SearchResult struct {
	Total      int             `csv:"total"`
	Records    []*SearchRecord `csv:"records"`
	RecordsMap map[string]*SearchRecord
}

type SearchRecord struct {
	Login         string `csv:"login"`
	Name          string `csv:"name"`
	Email         string `csv:"email"`
	Phone         string `csv:"phone"`
	KN            string `csv:"kn"`
	PlotNumber    string `csv:"zu"`
	IsMember      string `csv:"member"`
	Description   string `csv:"description"`
	IsNotifyBySMS string `csv:"notification_sms"`
	IsDebtor      string `csv:"debtor"`
	Debt          string `csv:"deb_sum"`
	SentLogin     string `csv:"sent_login"`
	LastEnter     string `csv:"last_enter"` // 25.07.2022 13:30
}

func (r SearchRecord) LastEnterTime() time.Time {
	var t time.Time
	if len(r.LastEnter) == 0 {
		return t
	}
	t, _ = time.ParseInLocation("02.01.2006 15:04", r.LastEnter, Location)
	return t
}

type Registry struct {
	registry map[string]*RegistryRecord
	search   *SearchResult
}

func loadRegistry(dir string) *Registry {
	records := LoadRegistryRecords(dir)
	if records != nil {
		users := LoadRegistryUsers(dir)
		for _, u := range users {
			reg := records[u.PlotNumber]
			if reg != nil {
				reg.Login = u.Login
			}
		}
	}
	searchResult, _ := search("")

	return &Registry{registry: records, search: searchResult}
}
