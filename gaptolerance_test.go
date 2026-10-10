package fitactivity

import (
	"testing"
	"time"
)

// A flight reported every half-minute interpolates across its own rhythm;
// a recording every second keeps the default.
func TestGapTolerance(t *testing.T) {
	at := func(sec ...int) *Track {
		tr := &Track{}
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, s := range sec {
			tr.Samples = append(tr.Samples, Sample{Time: base.Add(time.Duration(s) * time.Second)})
		}
		return tr
	}
	var everySecond, flight []int
	for i := range 100 {
		everySecond = append(everySecond, i)
	}
	// A dense taxi, then a cruise every 32 s with one coverage hole.
	for i := range 30 {
		flight = append(flight, 2*i)
	}
	for i := range 60 {
		flight = append(flight, 60+32*i)
	}
	flight = append(flight, flight[len(flight)-1]+900)
	if got := at(everySecond...).GapTolerance(); got != DefaultMaxGap {
		t.Errorf("1 Hz: %v, want %v", got, DefaultMaxGap)
	}
	if got := at(flight...).GapTolerance(); got != 64*time.Second {
		t.Errorf("a flight: %v, want 64s, twice its cruise rhythm", got)
	}
	if got := (&Track{}).GapTolerance(); got != DefaultMaxGap {
		t.Errorf("empty: %v", got)
	}

	// MaxGap set answers in place of the rhythm, and the hole of 900 s is
	// drawn across.
	bridged := at(flight...)
	bridged.MaxGap = time.Hour
	if got := bridged.GapTolerance(); got != time.Hour {
		t.Errorf("MaxGap an hour: %v", got)
	}
	last := bridged.Samples[len(bridged.Samples)-1].Time
	if _, ok := bridged.AtWithGap(last.Add(-450*time.Second), bridged.GapTolerance()); !ok {
		t.Error("the coverage hole was not bridged under MaxGap")
	}
}
