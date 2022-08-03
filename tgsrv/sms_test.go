package tgsrv

import (
	"testing"
	"time"
)

type SMSSendingLoopImpl struct {
	smses     SMSesDAO
	smsClient SMSSender
	abort     chan struct{}
	smsCnt    int
}

func (b *SMSSendingLoopImpl) smsesDAO() SMSesDAO {
	return b.smses
}

func (b *SMSSendingLoopImpl) smsSender() SMSSender {
	return b.smsClient
}

func (b *SMSSendingLoopImpl) abortChan() chan struct{} {
	return b.abort
}

func (b *SMSSendingLoopImpl) ListNew() ([]SMS, error) {
	var smses []SMS
	for i := 0; i < 1000; i++ {
		smses = append(smses, SMS{ID: i})
	}
	return smses, nil
}

func (b *SMSSendingLoopImpl) Update(sms SMS) error {
	return nil
}

func (b *SMSSendingLoopImpl) sendSMS(phone string, sms string) bool {
	b.smsCnt++
	return true
}

func TestSmsSenderLoopRates(t *testing.T) {
	{
		rates := []Rate{
			{time.Millisecond, 1},
			{time.Second, 10},
		}
		want := 10
		testSmsSenderLoopRates(t, rates, want)
	}
	{
		rates := []Rate{
			{time.Millisecond, 1},
			{time.Millisecond * 200, 10},
		}
		want := 30
		testSmsSenderLoopRates(t, rates, want)
	}
	{
		rates := []Rate{
			{time.Millisecond, 1},
			{time.Millisecond * 367, 10},
		}
		want := 20
		testSmsSenderLoopRates(t, rates, want)
	}
	{
		rates := []Rate{
			{time.Millisecond, 1},
			{time.Millisecond * 100, 2},
			{time.Second, 10},
		}
		want := 10
		testSmsSenderLoopRates(t, rates, want)
	}
}

func testSmsSenderLoopRates(t *testing.T, rates []Rate, want int) {
	abort := make(chan struct{})
	loop := &SMSSendingLoopImpl{abort: abort}
	loop.smses = loop
	loop.smsClient = loop

	done := make(chan struct{})
	go smsSenderLoopRates(loop, rates, done)
	<-time.NewTimer(time.Millisecond * 500).C
	close(abort)
	got := loop.smsCnt
	<-done
	if want != got {
		t.Errorf("want %v, got %v", want, got)
	}
}
