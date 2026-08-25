package fitactivity

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/wisborg/fitactivity/fittest"
)

// timerFixtureOptions is fixtureOptions (decodefixture_test.go) with a round
// number of seconds, so the expected Elapsed/Active durations below are easy
// to state independently of Build's own arithmetic: an 11-minute (660 s)
// fixture running one record per second has a wall-clock total of 659 s
// (Count-1, per Options.Count's doc comment).
func timerFixtureOptions() fittest.Options {
	opts := fittest.DefaultOptions()
	opts.Count = 660
	return opts
}

// TestTimerModel_ElapsedClampsBeforeStartAndAfterEnd pins the requirement
// the whole feature exists for: a video frame before the FIT's start reads
// 0:00:00, and one after its end reads the final total -- both are the same
// clamp (clampDuration), which is the point of writing it as one rule rather
// than two separate "before"/"after" branches that could disagree.
func TestTimerModel_ElapsedClampsBeforeStartAndAfterEnd(t *testing.T) {
	opts := timerFixtureOptions()
	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)

	total := time.Duration(opts.Count-1) * time.Second

	cases := []struct {
		name string
		at   time.Time
		want time.Duration
	}{
		{"an hour before start", opts.Start.Add(-time.Hour), 0},
		{"exactly at start", opts.Start, 0},
		{"midway through", opts.Start.Add(200 * time.Second), 200 * time.Second},
		{"exactly at end", opts.Start.Add(total), total},
		{"an hour after end", opts.Start.Add(total).Add(time.Hour), total},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := model.Elapsed(c.at); got != c.want {
				t.Errorf("Elapsed(%v) = %v, want %v", c.at, got, c.want)
			}
		})
	}

	if model.HasTimerEvents() != true {
		t.Error("HasTimerEvents() = false, want true -- every fixture carries a baseline start/stop_all pair")
	}
	// No Pauses were set, so there is nothing to subtract: Active must agree
	// with Elapsed at every instant tested above.
	for _, c := range cases {
		if got := model.Active(c.at); got != c.want {
			t.Errorf("Active(%v) = %v, want %v (equal to Elapsed: the fixture has no pauses)", c.at, got, c.want)
		}
	}
}

// TestTimerModel_ActiveFreezesDuringAPauseThenResumes derives its expected
// numbers independently of BuildTimerModel's implementation: a 60-second
// pause from t=100s to t=160s means Active must read 100s for every instant
// in [100s, 160s] (frozen at the pause's own start) and Elapsed-60s for
// every instant after it -- while Elapsed keeps counting wall-clock time
// throughout, pause or not.
func TestTimerModel_ActiveFreezesDuringAPauseThenResumes(t *testing.T) {
	opts := timerFixtureOptions()
	opts.Pauses = []fittest.Pause{{Start: 100 * time.Second, End: 160 * time.Second}}

	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)

	cases := []struct {
		name        string
		offset      time.Duration
		wantElapsed time.Duration
		wantActive  time.Duration
	}{
		{"just before the pause", 100 * time.Second, 100 * time.Second, 100 * time.Second},
		{"mid-pause", 130 * time.Second, 130 * time.Second, 100 * time.Second},
		{"right at resume", 160 * time.Second, 160 * time.Second, 100 * time.Second},
		{"after resuming", 200 * time.Second, 200 * time.Second, 140 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := opts.Start.Add(c.offset)
			if got := model.Elapsed(at); got != c.wantElapsed {
				t.Errorf("Elapsed(start+%v) = %v, want %v", c.offset, got, c.wantElapsed)
			}
			if got := model.Active(at); got != c.wantActive {
				t.Errorf("Active(start+%v) = %v, want %v", c.offset, got, c.wantActive)
			}
		})
	}

	// The records fittest omits inside the pause window also exercise
	// Track.At's placeholder path -- a sample squarely inside [100s,160s]
	// must come back not-ok, since the fixture wrote no record there.
	if _, ok := track.At(opts.Start.Add(130 * time.Second)); ok {
		t.Error("Track.At(mid-pause) returned ok=true, want false -- fittest.Options.Pauses must omit records inside the pause")
	}
}

// TestTimerModel_TwoPausesSubtractBothAndMatchTheSessionTotal covers two
// non-adjacent pauses, and cross-checks Active's event-derived total against
// TotalTimerTime, a NUMBER FROM A COMPLETELY DIFFERENT PATH: fittest writes
// TotalTimerTime as elapsedSeconds-pausedSeconds directly (see fittest.Build),
// while TimerModel.Active derives its number by walking the timer start/stop
// events instead. The two must agree, or the gauge and the FIT's own summary
// would print different numbers for the same activity.
func TestTimerModel_TwoPausesSubtractBothAndMatchTheSessionTotal(t *testing.T) {
	opts := timerFixtureOptions()
	opts.Pauses = []fittest.Pause{
		{Start: 100 * time.Second, End: 160 * time.Second}, // 60s
		{Start: 300 * time.Second, End: 330 * time.Second}, // 30s
	}

	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !track.Timing.HasTotals {
		t.Fatal("Timing.HasTotals = false, want true")
	}
	model := BuildTimerModel(track)

	total := time.Duration(opts.Count-1) * time.Second
	end := opts.Start.Add(total)

	wantActiveAtEnd := track.Timing.TotalTimer
	if got := model.Active(end); got != wantActiveAtEnd {
		t.Errorf("Active(end) = %v, want %v (Track.Timing.TotalTimer, the session's own figure)", got, wantActiveAtEnd)
	}

	// Independently: 659s elapsed minus 90s of pauses is 569s.
	const wantSeconds = 569
	if wantActiveAtEnd != wantSeconds*time.Second {
		t.Fatalf("test's own arithmetic is wrong: TotalTimer = %v, want %v", wantActiveAtEnd, wantSeconds*time.Second)
	}

	// Between the two pauses, only the first has been subtracted yet.
	between := opts.Start.Add(250 * time.Second)
	if got, want := model.Active(between), 190*time.Second; got != want { // 250 - 60
		t.Errorf("Active(between the pauses) = %v, want %v", got, want)
	}
}

// TestTimerModel_NoTimerEventsMeansActiveEqualsElapsed covers a file that
// carries session totals (so Elapsed has a real total to clamp to) but no
// `timer` events at all -- an older or partial file. With nothing to
// subtract, Active must equal Elapsed everywhere, and HasTimerEvents must
// say so explicitly rather than leave a caller inferring it from an empty
// pause list (see TimerModel.HasTimerEvents's doc comment on why the two
// "no pauses" cases need to be told apart).
func TestTimerModel_NoTimerEventsMeansActiveEqualsElapsed(t *testing.T) {
	start := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	track := &Track{
		Samples: []Sample{{Time: start}, {Time: start.Add(20 * time.Minute)}},
		Timing: ActivityTiming{
			Start:        start,
			TotalElapsed: 20 * time.Minute,
			TotalTimer:   20 * time.Minute,
			HasTotals:    true,
			// Events deliberately left nil.
		},
	}
	model := BuildTimerModel(track)

	if model.HasTimerEvents() {
		t.Error("HasTimerEvents() = true, want false -- this Track carries no Events")
	}
	for _, offset := range []time.Duration{-time.Minute, 0, 5 * time.Minute, 20 * time.Minute, 25 * time.Minute} {
		at := start.Add(offset)
		elapsed, active := model.Elapsed(at), model.Active(at)
		if elapsed != active {
			t.Errorf("at start+%v: Elapsed = %v, Active = %v, want equal (no timer events to derive a pause from)", offset, elapsed, active)
		}
	}
}

// TestTimerModel_ReadsTheInstantNotTheWallClockFields pins that a lookup
// instant's LOCATION cannot move the answer: the same moment expressed in
// two zones must measure the same elapsed and active time.
//
// This replaces an end-to-end version that rendered two clips under
// different --hud-timezone values and compared their clock pixels. That test
// cost two ffmpeg encodes and could not fail: the telemetry-hud effect's
// choice of the FIT-clock instant over the timezone-shifted one is not a
// behavioural difference at all, because time.Time.In changes a time's
// location and not the instant it names, and everything below measures with
// Sub, which is instant arithmetic. Verified directly -- swapping the two at
// that call site left every pixel identical.
//
// What remains worth pinning is the property one level down, HERE, where a
// regression could actually be written: an implementation that reached for a
// time's wall-clock FIELDS (Hour/Minute, a Format round-trip) instead of
// measuring the instant would make the zone matter, and would fail this.
// A non-whole-hour zone is used so such a slip cannot cancel out by landing
// on the same minute.
func TestTimerModel_ReadsTheInstantNotTheWallClockFields(t *testing.T) {
	start := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	track := &Track{
		Samples: []Sample{{Time: start}, {Time: start.Add(30 * time.Minute)}},
		Timing: ActivityTiming{
			Start:        start,
			TotalElapsed: 30 * time.Minute,
			TotalTimer:   20 * time.Minute,
			HasTotals:    true,
			Events: []TimerEvent{
				{Time: start, Start: true},
				{Time: start.Add(5 * time.Minute)},
				{Time: start.Add(15 * time.Minute), Start: true},
			},
		},
	}
	model := BuildTimerModel(track)

	kathmandu := time.FixedZone("+05:45", 5*3600+45*60)
	for _, offset := range []time.Duration{0, 3 * time.Minute, 10 * time.Minute, 25 * time.Minute} {
		at := start.Add(offset)
		shifted := at.In(kathmandu)
		if got, want := model.Elapsed(shifted), model.Elapsed(at); got != want {
			t.Errorf("at start+%v: Elapsed of the +05:45 spelling = %v, of the UTC spelling = %v -- the same instant must measure the same", offset, got, want)
		}
		if got, want := model.Active(shifted), model.Active(at); got != want {
			t.Errorf("at start+%v: Active of the +05:45 spelling = %v, of the UTC spelling = %v -- the same instant must measure the same", offset, got, want)
		}
	}
}

// TestTimerModel_NoSessionTotalsFallsBackToTheLastSample covers a file with
// no Session totals at all (HasTotals false) -- BuildTimerModel must derive
// End from the last sample's time rather than leaving it at Start (which
// would make every Elapsed after the first sample clamp straight to 0).
func TestTimerModel_NoSessionTotalsFallsBackToTheLastSample(t *testing.T) {
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	last := start.Add(5 * time.Minute)
	track := &Track{
		Samples: []Sample{{Time: start}, {Time: start.Add(time.Minute)}, {Time: last}},
		Timing:  ActivityTiming{Start: start}, // HasTotals left false
	}
	model := BuildTimerModel(track)

	if got, want := model.Elapsed(last.Add(time.Hour)), 5*time.Minute; got != want {
		t.Errorf("Elapsed long after the last sample = %v, want %v (End should fall back to the last sample's time)", got, want)
	}
	if got, want := model.Elapsed(start.Add(2*time.Minute)), 2*time.Minute; got != want {
		t.Errorf("Elapsed mid-activity = %v, want %v", got, want)
	}
}

// TestBuildTimerModel_EndComesFromTotalElapsedNotSessionTimestamp pins the
// one trap ActivityTiming's doc comment names, using a fixture that
// reproduces it directly rather than trusting Decode never regresses into
// reading Timestamp: session.Timestamp decodes EQUAL to StartTime on real
// Garmin files (probed against both files in test_videos/), so it must never
// be read as the activity's end. This file sets Timestamp = StartTime
// explicitly -- the exact real-file shape -- and checks the derived end
// still comes out as Start + TotalElapsed, not Start (which is what reading
// Timestamp here would produce, permanently clamping Elapsed to 0).
func TestBuildTimerModel_EndComesFromTotalElapsedNotSessionTimestamp(t *testing.T) {
	start := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	const totalElapsedSeconds = 1800.0 // 30 minutes

	act := &filedef.Activity{
		FileId: *mesgdef.NewFileId(nil).
			SetType(typedef.FileActivity).
			SetManufacturer(typedef.ManufacturerGarmin).
			SetProduct(0).
			SetSerialNumber(0).
			SetTimeCreated(start),
		Activity: mesgdef.NewActivity(nil).
			SetTimestamp(start).
			SetNumSessions(1).
			SetType(typedef.ActivityManual).
			SetEvent(typedef.EventActivity).
			SetEventType(typedef.EventTypeStop),
		Sessions: []*mesgdef.Session{
			mesgdef.NewSession(nil).
				SetTimestamp(start). // the real-file bug shape: Timestamp == StartTime
				SetStartTime(start).
				SetSport(typedef.SportRunning).
				SetEvent(typedef.EventSession).
				SetEventType(typedef.EventTypeStop).
				SetTotalElapsedTimeScaled(totalElapsedSeconds).
				SetTotalTimerTimeScaled(totalElapsedSeconds),
		},
		Records: []*mesgdef.Record{
			mesgdef.NewRecord(nil).SetTimestamp(start),
		},
	}

	fit := act.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&fit); err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "timestamp-equals-start.fit")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	track, err := Decode(path)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !track.Timing.Start.Equal(start) {
		t.Fatalf("Timing.Start = %v, want %v", track.Timing.Start, start)
	}
	if !track.Timing.HasTotals {
		t.Fatal("Timing.HasTotals = false, want true")
	}

	model := BuildTimerModel(track)
	wantTotal := time.Duration(totalElapsedSeconds) * time.Second
	if got := model.Elapsed(start.Add(24 * time.Hour)); got != wantTotal {
		t.Errorf("Elapsed far past the end = %v, want %v -- BuildTimerModel must derive End from Start+TotalElapsed, "+
			"not session.Timestamp (which this fixture deliberately set equal to Start)", got, wantTotal)
	}
}

// buildMinimalActivityFIT encodes a minimal, valid FIT Activity file to path,
// with no Session message at all when withSession is false, or with one
// carrying totals but no `timer` events when withSession is true -- neither
// shape is reachable through fitactivity/fittest, which always writes both a
// Session (see fittest.Build) and a baseline start/stop_all timer event pair
// (see fittest.buildTimerEvents), so Decode's OWN handling of an absent
// Session and an empty Events list is otherwise never exercised by anything
// built through the fixture package the rest of this file uses.
// buildMinimalActivityFIT writes an activity spanning start..last, optionally
// with a Session message. sessionTotal overrides the session's TotalElapsed
// when non-zero; zero derives it from last-start, which is what a device
// normally writes.
//
// The override exists because a fixture whose session total happens to land on
// its last record cannot distinguish "end comes from the session total" from
// "end comes from the last sample" -- both rules give the same answer, and a
// test asserting against it proves nothing about which one is implemented.
func buildMinimalActivityFIT(t *testing.T, start, last time.Time, withSession bool, sessionTotal time.Duration) string {
	t.Helper()

	act := &filedef.Activity{
		FileId: *mesgdef.NewFileId(nil).
			SetType(typedef.FileActivity).
			SetManufacturer(typedef.ManufacturerGarmin).
			SetProduct(0).
			SetSerialNumber(0).
			SetTimeCreated(start),
		Activity: mesgdef.NewActivity(nil).
			SetTimestamp(last).
			SetType(typedef.ActivityManual).
			SetEvent(typedef.EventActivity).
			SetEventType(typedef.EventTypeStop),
		Records: []*mesgdef.Record{
			mesgdef.NewRecord(nil).SetTimestamp(start),
			mesgdef.NewRecord(nil).SetTimestamp(last),
		},
		// Events deliberately omitted in both cases: this is what "the file
		// carries no `timer` events" looks like, whether or not it has a
		// Session.
	}
	total := sessionTotal
	if total == 0 {
		total = last.Sub(start)
	}
	if withSession {
		act.Activity.SetNumSessions(1)
		act.Sessions = []*mesgdef.Session{
			mesgdef.NewSession(nil).
				SetTimestamp(last).
				SetStartTime(start).
				SetSport(typedef.SportRunning).
				SetEvent(typedef.EventSession).
				SetEventType(typedef.EventTypeStop).
				SetTotalElapsedTimeScaled(total.Seconds()).
				SetTotalTimerTimeScaled(total.Seconds()),
		}
	} else {
		act.Activity.SetNumSessions(0)
	}

	fit := act.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&fit); err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	name := "with-session.fit"
	if !withSession {
		name = "no-session.fit"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDecode_NoSessionMessageFallsBackToSampleTimes covers a FIT Activity
// file that carries no Session message at all -- a shape a real Garmin
// device should never produce, but a truncated or hand-assembled file might,
// and nothing fitactivity/fittest builds can reach (fittest.Build always writes
// one). Decode must leave Timing at its zero value rather than erroring or
// inventing a Start from somewhere else, and BuildTimerModel must fall back
// to the first/last SAMPLE's time for its window -- the same fallback
// TestTimerModel_NoSessionTotalsFallsBackToTheLastSample exercises against a
// hand-built Track, but this is the test that proves DECODE ITSELF produces
// that zero Timing rather than the fallback only being reachable by a test
// that bypasses Decode to construct it directly.
func TestDecode_NoSessionMessageFallsBackToSampleTimes(t *testing.T) {
	start := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	last := start.Add(4 * time.Minute)

	track, err := Decode(buildMinimalActivityFIT(t, start, last, false, 0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !track.Timing.Start.IsZero() {
		t.Errorf("Timing.Start = %v, want zero -- the file carries no Session message", track.Timing.Start)
	}
	if track.Timing.HasTotals {
		t.Error("Timing.HasTotals = true, want false -- the file carries no Session totals")
	}
	if len(track.Timing.Events) != 0 {
		t.Errorf("Timing.Events = %v, want empty -- the file carries no `timer` events", track.Timing.Events)
	}

	model := BuildTimerModel(track)
	if model.HasTimerEvents() {
		t.Error("HasTimerEvents() = true, want false")
	}
	if got, want := model.Elapsed(start.Add(-time.Hour)), time.Duration(0); got != want {
		t.Errorf("Elapsed before the first sample = %v, want %v", got, want)
	}
	if got, want := model.Elapsed(start.Add(2*time.Minute)), 2*time.Minute; got != want {
		t.Errorf("Elapsed mid-activity = %v, want %v -- Start should fall back to the first sample's time", got, want)
	}
	if got, want := model.Elapsed(last.Add(time.Hour)), 4*time.Minute; got != want {
		t.Errorf("Elapsed after the last sample = %v, want %v -- End should fall back to the last sample's time", got, want)
	}
	// With no pauses to find (there are no Events at all), Active must agree
	// with Elapsed at every instant, exactly as TimerModel.Active's doc
	// comment promises for a file with no timer events.
	if got, want := model.Active(start.Add(2*time.Minute)), model.Elapsed(start.Add(2*time.Minute)); got != want {
		t.Errorf("Active = %v, want %v (equal to Elapsed: no timer events to derive a pause from)", got, want)
	}
}

// TestDecode_SessionWithNoTimerEventsLeavesHasTimerEventsFalse covers a FIT
// Activity file whose Session carries totals but whose file has no `timer`
// events at all -- an older FIT profile, or a device/exporter that never
// wrote one. fitactivity/fittest cannot build this shape either: fittest.Build
// always writes at least a start/stop_all pair (see buildTimerEvents), so
// Decode's Events loop running over a genuinely empty act.Events is
// otherwise only reachable by constructing the file by hand, as this test
// does. Without it, a Decode regression that panicked or mis-set HasTotals
// on an Events-less file could only be caught by a probe test that skips in
// `make test`.
func TestDecode_SessionWithNoTimerEventsLeavesHasTimerEventsFalse(t *testing.T) {
	start := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	last := start.Add(4 * time.Minute)

	track, err := Decode(buildMinimalActivityFIT(t, start, last, true, 0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !track.Timing.HasTotals {
		t.Fatal("Timing.HasTotals = false, want true -- the fixture's Session sets both totals")
	}
	if len(track.Timing.Events) != 0 {
		t.Errorf("Timing.Events = %v, want empty -- the fixture carries no `timer` events", track.Timing.Events)
	}

	model := BuildTimerModel(track)
	if model.HasTimerEvents() {
		t.Error("HasTimerEvents() = true, want false -- this file carries no `timer` events")
	}
	for _, offset := range []time.Duration{-time.Minute, 0, 2 * time.Minute, 4 * time.Minute, 10 * time.Minute} {
		at := start.Add(offset)
		if elapsed, active := model.Elapsed(at), model.Active(at); elapsed != active {
			t.Errorf("at start+%v: Elapsed = %v, Active = %v, want equal (no timer events to derive a pause from)", offset, elapsed, active)
		}
	}
}

// TestBuildPauses_MergesAdjacentPairsAndClipsToTheWindow pins buildPauses's
// own structural claims directly, bypassing BuildTimerModel and Decode
// entirely: nothing built through fitactivity/fittest ever produces adjacent or
// out-of-window timer events (fittest.Options.Pauses documents that its
// Pauses must already be sorted, non-overlapping, and within range), so the
// merge-on-touch and clip-to-window branches buildPauses's own doc comment
// describes are otherwise dead code as far as this test suite is concerned.
// A regression here would silently mis-total Active for a real file whose
// device wrote a duplicate or boundary-straddling timer event -- exactly the
// kind of guard this feature's tests are supposed to prove ACTS, not merely
// exists.
func TestBuildPauses_MergesAdjacentPairsAndClipsToTheWindow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	ev := func(s int, start bool) TimerEvent { return TimerEvent{Time: at(s), Start: start} }

	t.Run("two separated pauses stay separate", func(t *testing.T) {
		events := []TimerEvent{ev(10, false), ev(20, true), ev(50, false), ev(60, true)}
		got := buildPauses(events, at(0), at(100))
		want := []pause{{at(10), at(20)}, {at(50), at(60)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildPauses = %v, want %v", got, want)
		}
	})

	t.Run("two pauses that touch at the boundary merge into one", func(t *testing.T) {
		// stop@10..start@20 immediately followed by stop@20..start@30: a
		// device that stops and restarts at the same instant, or a
		// resolution-limited duplicate pair, must read as one continuous
		// pause [10,30], not two adjacent ones -- Active must not count the
		// zero-width gap at 20s as "running".
		events := []TimerEvent{ev(10, false), ev(20, true), ev(20, false), ev(30, true)}
		got := buildPauses(events, at(0), at(100))
		want := []pause{{at(10), at(30)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildPauses = %v, want %v (adjacent pauses must merge)", got, want)
		}
	})

	t.Run("a pause before the window clips, one entirely after it is dropped", func(t *testing.T) {
		// [0,15] clips to [10,15] (window starts at 10s); [95,120] would
		// clip its end to 90 (window end) but its start (95) is already
		// past that once clipped, so it must be dropped entirely rather
		// than kept as a pause with a negative or inverted span.
		events := []TimerEvent{ev(0, false), ev(15, true), ev(95, false), ev(120, true)}
		got := buildPauses(events, at(10), at(90))
		want := []pause{{at(10), at(15)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildPauses = %v, want %v", got, want)
		}
	})

	t.Run("a duplicate stop with no intervening start keeps the earlier stopAt", func(t *testing.T) {
		events := []TimerEvent{ev(10, false), ev(15, false), ev(20, true)}
		got := buildPauses(events, at(0), at(100))
		want := []pause{{at(10), at(20)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildPauses = %v, want %v (the pause must start at the FIRST stop event, not the second)", got, want)
		}
	})
}

// TestTimerModel_PausedMarksTheIntervalActiveSubtracts checks Paused against
// hand-derived offsets on a fixture with a known pause, including both
// boundaries.
//
// The boundaries are the whole point of the table. Paused is half-open --
// [start, end) -- so the instant a pause begins is paused and the instant it
// ends is not, and those two rows are the ones a re-implementation gets wrong.
func TestTimerModel_PausedMarksTheIntervalActiveSubtracts(t *testing.T) {
	opts := timerFixtureOptions()
	opts.Pauses = []fittest.Pause{{Start: 100 * time.Second, End: 160 * time.Second}}

	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)
	start, _ := track.Coverage()

	cases := []struct {
		name   string
		offset time.Duration
		want   bool
	}{
		{"before the pause", 99 * time.Second, false},
		{"exactly at the pause's start", 100 * time.Second, true},
		{"one second in", 101 * time.Second, true},
		{"mid-pause", 130 * time.Second, true},
		{"one second before resuming", 159 * time.Second, true},
		// Sub-second offsets are the normal case for a caller rendering video
		// -- at 30 fps almost no frame lands on a whole second -- and this row
		// is where the local derivation a caller might reach for goes wrong.
		// Active(159.5s+1s) is 100.5s against Active(159.5s) of 100s, so the
		// derivative is non-zero and reads "running" half a second before the
		// pause actually ends.
		{"half a second before resuming", 159*time.Second + 500*time.Millisecond, true},
		{"exactly at the pause's end", 160 * time.Second, false},
		{"after the pause", 200 * time.Second, false},
		// Outside the activity's window: not running, but not paused either.
		{"before the activity started", -30 * time.Second, false},
		{"after the activity ended", 10000 * time.Second, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := model.Paused(start.Add(c.offset)); got != c.want {
				t.Errorf("Paused(start+%v) = %v, want %v", c.offset, got, c.want)
			}
		})
	}
}

// TestTimerModel_PausedAgreesWithActiveAcrossTheWholeActivity is the test that
// makes Paused worth having upstream rather than derived by a caller.
//
// The property: Active advances at exactly the instants Paused reports as
// running, and freezes at exactly the instants it reports as paused. Sweeping
// the whole activity a second at a time and checking the two against each
// other means the pair cannot drift -- which is the failure the local
// derivation (Active(at+1s) == Active(at)) would eventually have produced,
// since it reads one second early at every boundary.
//
// Note this is deliberately NOT how Paused is implemented. Asserting that two
// independent readings of the same pause list agree is a real test; asserting
// that a function agrees with itself is not.
func TestTimerModel_PausedAgreesWithActiveAcrossTheWholeActivity(t *testing.T) {
	opts := timerFixtureOptions()
	opts.Pauses = []fittest.Pause{
		{Start: 100 * time.Second, End: 160 * time.Second},
		{Start: 400 * time.Second, End: 430 * time.Second},
	}

	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)
	start, end := track.Coverage()

	const step = time.Second
	pausedSeconds, runningSeconds := 0, 0
	for at := start; at.Before(end); at = at.Add(step) {
		advanced := model.Active(at.Add(step)) > model.Active(at)
		paused := model.Paused(at)
		if paused == advanced {
			t.Fatalf("at start+%v: Paused = %v but Active %s over the next second",
				at.Sub(start), paused,
				map[bool]string{true: "advanced", false: "froze"}[advanced])
		}
		if paused {
			pausedSeconds++
		} else {
			runningSeconds++
		}
	}

	// Guard against the sweep proving nothing because one side never occurred:
	// a Paused that always returned false would agree with an Active that
	// always advanced, and the loop above would pass without either being
	// exercised.
	if pausedSeconds == 0 || runningSeconds == 0 {
		t.Fatalf("sweep saw %d paused and %d running seconds; it must see both to mean anything",
			pausedSeconds, runningSeconds)
	}
	// 90 seconds of pause were built into the fixture. Derived from the
	// options above, not read off a run.
	if want := 90; pausedSeconds != want {
		t.Errorf("sweep saw %d paused seconds, want %d", pausedSeconds, want)
	}
}

// TestTimerModel_PausedIsFalseWithoutTimerEvents pins the case a caller must
// not mistake for "never stopped": a file carrying no timer events has no
// pauses to find, so Paused is false everywhere. HasTimerEvents is what tells
// that apart from an activity that genuinely ran without stopping.
func TestTimerModel_PausedIsFalseWithoutTimerEvents(t *testing.T) {
	track, err := Decode(buildMinimalActivityFIT(t,
		time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
		time.Date(2020, 1, 2, 3, 14, 5, 0, time.UTC), true, 0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)
	if model.HasTimerEvents() {
		t.Fatal("fixture unexpectedly carries timer events; this test needs one without")
	}
	start, end := track.Coverage()
	for at := start; !at.After(end); at = at.Add(time.Minute) {
		if model.Paused(at) {
			t.Fatalf("Paused(start+%v) = true on a file with no timer events", at.Sub(start))
		}
	}
}

// TestTimerModel_WindowIsWhatElapsedMeasuresFrom pins Window against Elapsed
// rather than against literals.
//
// The two must describe the same interval, because the whole reason Window
// exists is to stop a caller rebuilding that interval and drifting from it.
// Asserting Elapsed(start) == 0 and Elapsed(end) == end-start ties them
// together: any change that moved one without the other fails here.
func TestTimerModel_WindowIsWhatElapsedMeasuresFrom(t *testing.T) {
	opts := timerFixtureOptions()
	opts.Pauses = []fittest.Pause{{Start: 100 * time.Second, End: 160 * time.Second}}

	track, err := Decode(buildFixture(t, opts))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	model := BuildTimerModel(track)
	start, end := model.Window()

	if start.IsZero() || end.IsZero() {
		t.Fatalf("Window returned a zero instant: %v..%v", start, end)
	}
	if !end.After(start) {
		t.Fatalf("Window end %v is not after start %v", end, start)
	}
	if got := model.Elapsed(start); got != 0 {
		t.Errorf("Elapsed(window start) = %v, want 0", got)
	}
	if got, want := model.Elapsed(end), end.Sub(start); got != want {
		t.Errorf("Elapsed(window end) = %v, want %v (the window's own span)", got, want)
	}
	// And the clamp: an instant past the end reads the same total, which is
	// what makes the end the end.
	if got, want := model.Elapsed(end.Add(time.Hour)), end.Sub(start); got != want {
		t.Errorf("Elapsed(end+1h) = %v, want %v", got, want)
	}
	if got := model.Elapsed(start.Add(-time.Hour)); got != 0 {
		t.Errorf("Elapsed(start-1h) = %v, want 0", got)
	}
}

// TestTimerModel_WindowPrefersTheSessionTotalOverTheLastSample pins the
// resolution rule itself -- the part a caller would otherwise have to rebuild,
// and the part where rebuilding it wrong is silent.
//
// The fixture's session declares a total THIRTY SECONDS LONGER than its last
// record, which is what makes the test discriminating: "end is start plus the
// session total" and "end is the last sample" give different answers here, and
// only the first is correct. An earlier version of this test used a fixture
// where the two coincided and could not tell them apart -- it said so in a log
// line, which is a test admitting it proves nothing.
func TestTimerModel_WindowPrefersTheSessionTotalOverTheLastSample(t *testing.T) {
	const (
		recorded = 10 * time.Minute
		declared = recorded + 30*time.Second
	)
	start := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	last := start.Add(recorded)

	track, err := Decode(buildMinimalActivityFIT(t, start, last, true, declared))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !track.Timing.HasTotals {
		t.Fatal("precondition: the fixture's session must carry totals")
	}
	model := BuildTimerModel(track)
	gotStart, gotEnd := model.Window()

	if !gotStart.Equal(start) {
		t.Errorf("Window start = %v, want the session's start_time %v", gotStart, start)
	}
	if want := start.Add(declared); !gotEnd.Equal(want) {
		t.Errorf("Window end = %v, want start+TotalElapsed %v", gotEnd, want)
	}
	// The assertion that makes the one above mean something.
	if gotEnd.Equal(last) {
		t.Errorf("Window end fell on the last sample (%v); the session total should have won", last)
	}
	if got := model.Elapsed(gotEnd); got != declared {
		t.Errorf("Elapsed(window end) = %v, want the declared total %v", got, declared)
	}
}

// TestTimerModel_WindowFallsBackToTheSamplesWithoutSessionTotals covers the
// other arm of the rule, on a file whose session carries no totals.
func TestTimerModel_WindowFallsBackToTheSamplesWithoutSessionTotals(t *testing.T) {
	start := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	last := start.Add(10 * time.Minute)
	track, err := Decode(buildMinimalActivityFIT(t, start, last, false, 0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if track.Timing.HasTotals {
		t.Fatal("precondition: this fixture must carry no session totals")
	}
	model := BuildTimerModel(track)
	gotStart, gotEnd := model.Window()

	first, lastSample := track.Coverage()
	if !gotStart.Equal(first) {
		t.Errorf("Window start = %v, want the first sample %v", gotStart, first)
	}
	if !gotEnd.Equal(lastSample) {
		t.Errorf("Window end = %v, want the last sample %v", gotEnd, lastSample)
	}
}
