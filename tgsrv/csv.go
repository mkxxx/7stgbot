package tgsrv

import (
	"github.com/gocarina/gocsv"
	"os"
	"path/filepath"
)

func LoadElectrForMonth(dir string, year, month int) map[string]*ElectrEvidence {
	fp := filepath.Join(dir, "electr_2022-04.csv")
	f, err := os.OpenFile(fp, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		Logger.Errorf("error opening %s %v", fp, err)
	}
	defer f.Close()

	var items []*ElectrEvidence
	if err := gocsv.UnmarshalFile(f, &items); err != nil {
		Logger.Errorf("error unmarshaling csv %s %v", fp, err)
	}
	m := make(map[string]*ElectrEvidence)
	for _, it := range items {
		m[it.PlotNumber] = it
	}
	return m
}

type ElectrEvidence struct {
	N               string `csv:"N"`
	Prepaid         string `csv:"prepaid"`
	LastPaymentDate string `csv:"last_payment_date"`
	PlotNumber      string `csv:"plot_number"`
	CurrEvidence    string `csv:"curr_evidence"`
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
