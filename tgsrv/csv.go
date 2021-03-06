package tgsrv

import (
	"fmt"
	"github.com/gocarina/gocsv"
	"os"
	"path/filepath"
	"strconv"
)

func LoadElectrForMonth(dir string, year, month int) []*ElectrEvidence {
	fname := electrFileName(year, month)
	fp := filepath.Join(dir, fname)
	f, err := os.OpenFile(fp, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		Logger.Errorf("error opening %s %v", fp, err)
		return nil
	}
	defer f.Close()

	Logger.Infof("loading file %s", fp)

	var items []*ElectrEvidence
	if err := gocsv.UnmarshalFile(f, &items); err != nil {
		Logger.Errorf("error unmarshaling csv %s %v", fp, err)
		return nil
	}
	Logger.Infof("loaded%d from %s", len(items), fp)
	return items
}

func toMap(items []*ElectrEvidence) map[string]*ElectrEvidence {
	m := make(map[string]*ElectrEvidence)
	for _, it := range items {
		m[it.PlotNumber] = it
	}
	return m
}

func electrFileName(year int, month int) string {
	return fmt.Sprintf("electr_%d-%02d.csv", year, month)
}

type ElectrEvidence struct {
	N               string `csv:"N"`
	FIO             string `csv:"FIO"`
	Prepaid         string `csv:"prepaid"`
	LastPaymentDate string `csv:"last_payment_date"`
	PlotNumber      string `csv:"plot_number"`
	CurrEvidence    string `csv:"curr_evidence"`
	CurrEvidence2   string `csv:"curr_evidence2"`
	PrevEvidence    string `csv:"prev_evidence"`
	Spent           string `csv:"spent"`
	Losses          string `csv:"losses"`
	SpentAmount     string `csv:"spent_amount"`
	LossesAmount    string `csv:"losses_amount"`
	Total           string `csv:"total"`
	PrevDebt        string `csv:"prev_debt"`
	CurrDebt        string `csv:"curr_debt"`
	QRURL           string `csv:"qr_url"`
	BotURL          string `csv:"bot_url"`
	NotUsed         string `csv:"-"`
}

func (e *ElectrEvidence) Copy() *ElectrEvidence {
	return &ElectrEvidence{
		N:               e.N,
		FIO:             e.FIO,
		Prepaid:         e.Prepaid,
		LastPaymentDate: e.LastPaymentDate,
		PlotNumber:      e.PlotNumber,
		CurrEvidence:    e.CurrEvidence,
		CurrEvidence2:   e.CurrEvidence2,
		PrevEvidence:    e.PrevEvidence,
		Spent:           e.Spent,
		Losses:          e.Losses,
		SpentAmount:     e.SpentAmount,
		LossesAmount:    e.LossesAmount,
		Total:           e.Total,
		PrevDebt:        e.PrevDebt,
		CurrDebt:        e.CurrDebt,
		QRURL:           e.QRURL,
	}
}

func (e *ElectrEvidence) prepaidMinusDebt() float64 {
	prepaid, err := strconv.ParseFloat(e.Prepaid, 64)
	if err != nil {
		prepaid = 0
	}
	debt, err := strconv.ParseFloat(e.PrevDebt, 64)
	if err != nil {
		debt = 0
	}
	return debt - prepaid
}

func (e *ElectrEvidence) prepaidMinusDebtAsStr() string {
	r := e.prepaidMinusDebt()
	if r == 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", r)
}

type RegistryRecord struct {
	PlotNumber          string `csv:"?????????? ??????????????"`
	FIO                 string `csv:"?????? ????????????????????????"`
	IsMember            string `csv:"???????? ??????"`
	IsPrivilegee        string `csv:"????????????????"`
	IsDebtor            string `csv:"??????????????"`
	Debt                string `csv:"?????????? ??????????"`
	IsNotifyBySMS       string `csv:"?????????????????????? ???? ??????"`
	CadastralNumber     string `csv:"?????????????????????? ?????????? ??????????????"`
	Phone               string `csv:"??????????????"`
	Email               string `csv:"?????????????????????? ??????????"`
	Region              string `csv:"???????????? ????????????????????"`
	RegistrationAddress string `csv:"?????????? ?????????????????????? ???? ?????????? ????????????????????"`
	PostAddress         string `csv:"???????????????? ??????????"`
	Privatization       string `csv:"?????????? ??????????"`
	RegistrationDate    string `csv:"?????????? ?? ???????? ??????????????????????"`
	Share               string `csv:"????????"`
	PersonalID          string `csv:"???????????????? ???????????????????????????? ????????????????"`
	Contacts            string `csv:"???????????????????????????? ???????????????????? ???????????? (??????. ???????? ?? ????????????)"`
	Comments            string `csv:"????????????????????"`
	Login               string `csv:"login"`
}

func LoadRegistryRecords(dir string) map[string]*RegistryRecord {
	fp := filepath.Join(dir, "reestr.csv")
	f, err := os.OpenFile(fp, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		Logger.Errorf("error opening %s %v", fp, err)
		return nil
	}
	defer f.Close()

	var items []*RegistryRecord
	if err := gocsv.UnmarshalFile(f, &items); err != nil {
		Logger.Errorf("error unmarshaling csv %s %v", fp, err)
		return nil
	}
	m := make(map[string]*RegistryRecord)
	for _, it := range items {
		m[it.PlotNumber] = it
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

type RegistryUser struct {
	Login      string `csv:"login"`
	PlotNumber string `csv:"plot_number"`
}

func LoadRegistryUsers(dir string) map[string]*RegistryUser {
	fp := filepath.Join(dir, "reestr_users.csv")
	f, err := os.OpenFile(fp, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		Logger.Errorf("error opening %s %v", fp, err)
		return nil
	}
	defer f.Close()

	var items []*RegistryUser
	if err := gocsv.UnmarshalFile(f, &items); err != nil {
		Logger.Errorf("error unmarshaling csv %s %v", fp, err)
		return nil
	}
	m := make(map[string]*RegistryUser)
	for _, it := range items {
		m[it.PlotNumber] = it
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
