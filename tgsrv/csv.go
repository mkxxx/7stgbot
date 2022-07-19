package tgsrv

import (
	"fmt"
	"github.com/gocarina/gocsv"
	"os"
	"path/filepath"
	"strconv"
)

func LoadElectrForMonth(dir string, year, month int) map[string]*ElectrEvidence {
	fname := fmt.Sprintf("electr_%d-%02d.csv", year, month)
	fp := filepath.Join(dir, fname)
	f, err := os.OpenFile(fp, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		Logger.Errorf("error opening %s %v", fp, err)
		return nil
	}
	defer f.Close()

	var items []*ElectrEvidence
	if err := gocsv.UnmarshalFile(f, &items); err != nil {
		Logger.Errorf("error unmarshaling csv %s %v", fp, err)
		return nil
	}
	m := make(map[string]*ElectrEvidence)
	for _, it := range items {
		m[it.PlotNumber] = it
	}
	if len(m) == 0 {
		return nil
	}
	return m
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
	NotUsed         string `csv:"-"`
}

func (e *ElectrEvidence) prepaidMinusDebt() string {
	prepaid, err := strconv.ParseFloat(e.Prepaid, 64)
	if err != nil {
		prepaid = 0
	}
	debt, err := strconv.ParseFloat(e.PrevDebt, 64)
	if err != nil {
		debt = 0
	}
	if debt-prepaid == 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", debt-prepaid)
}
