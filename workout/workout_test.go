package workout

import (
	"math"
	"testing"
	"time"

	"github.com/wisborg/fitactivity"
)

var t0 = time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)

// part is a stretch of a made-up workout: seconds long, at power watts
// and speed metres a second, climbing at grade; paused records nothing
// for its length.
type part struct {
	secs          int
	power, speed  float64
	grade         float64
	paused, noPow bool
}

// build is a track of parts, a sample a second, its power wobbling by a
// fixed pattern of up to fifteen watts as a runner's does stride to
// stride, its distance and elevation summed from its speed and grade.
func build(sport string, parts ...part) *fitactivity.Track {
	tr := &fitactivity.Track{Sport: sport}
	at, dist, elev := 0, 0.0, 100.0
	wobble := []float64{0, 9, -6, 15, -12, 4, -9, 12, -3, -15, 6, 10}
	for _, p := range parts {
		for i := 0; i < p.secs; i++ {
			at++
			dist += p.speed
			elev += p.speed * p.grade
			if p.paused {
				continue
			}
			s := fitactivity.Sample{Time: t0.Add(time.Duration(at) * time.Second),
				HasSpeed: true, Speed: p.speed, HasDistance: true, Distance: dist,
				HasElevation: true, Elevation: elev}
			if !p.noPow {
				s.HasPower = true
				s.Power = uint16(math.Max(0, p.power+wobble[at%len(wobble)]))
			}
			tr.Samples = append(tr.Samples, s)
		}
	}
	return tr
}

func work(secs int, power float64) part { return part{secs: secs, power: power, speed: 4} }
func easy(secs int, power float64) part { return part{secs: secs, power: power, speed: 3} }

// approx says d is within a few seconds of want.
func approx(d, want time.Duration) bool { return (d - want).Abs() <= 10*time.Second }

// A warm-up, six intervals of three minutes with a minute's recovery,
// and a cool-down is one set of six, each interval and recovery its own
// length, at an effort apart from the warm-up's.
func TestASetOfIntervals(t *testing.T) {
	parts := []part{easy(600, 200)}
	for i := 0; i < 6; i++ {
		parts = append(parts, work(180, 320))
		if i < 5 {
			parts = append(parts, easy(60, 120))
		}
	}
	parts = append(parts, easy(600, 200))
	a := Analyse(build("running", parts...), Options{})
	if a.Signal != SignalPower {
		t.Errorf("signal %q, want power", a.Signal)
	}
	if len(a.Sets) != 1 || len(a.Sets[0].Intervals) != 6 {
		t.Fatalf("sets %+v, want one of six", a.Sets)
	}
	s := a.Sets[0]
	for i, iv := range s.Intervals {
		if !approx(iv.Duration(), 3*time.Minute) || math.Abs(iv.Effort-320) > 10 || !iv.HasPower {
			t.Errorf("interval %d: %v at %.0f W, want 3m at 320", i, iv.Duration(), iv.Effort)
		}
		if d, ok := iv.Distance(); !ok || math.Abs(d-720) > 40 {
			t.Errorf("interval %d covered %.0f m, want 720", i, d)
		}
	}
	if len(s.Recoveries) != 5 || !approx(s.Recoveries[0].Duration(), time.Minute) {
		t.Errorf("recoveries %+v, want five of a minute", s.Recoveries)
	}
	if len(a.Levels) < 3 {
		t.Errorf("levels %+v, want recovery, warm-up and work apart", a.Levels)
	}
}

// A steady run stopped three times at the lights is no set: its stops are
// stops, not recoveries.
func TestSteadyWithStopsIsNoSet(t *testing.T) {
	stop := part{secs: 30, speed: 0, power: 0}
	a := Analyse(build("running", easy(900, 280), stop, easy(900, 280), stop, easy(900, 280), stop, easy(900, 280)), Options{})
	if len(a.Sets) != 0 {
		t.Errorf("sets %+v in a steady run", a.Sets)
	}
	if len(a.Stops) != 3 {
		t.Errorf("%d stops, want 3", len(a.Stops))
	}
	if len(a.Levels) != 1 {
		t.Errorf("levels %+v, want the one effort", a.Levels)
	}
}

// Intervals recovered from standing -- the watch paused between -- are a
// set, the pauses its recoveries.
func TestStandingRecoveries(t *testing.T) {
	var parts []part
	for i := 0; i < 5; i++ {
		parts = append(parts, work(120, 350))
		if i < 4 {
			parts = append(parts, part{secs: 90, paused: true})
		}
	}
	a := Analyse(build("running", parts...), Options{})
	if len(a.Sets) != 1 || len(a.Sets[0].Intervals) != 5 {
		t.Fatalf("sets %+v, want one of five", a.Sets)
	}
	if !a.Stops[0].Paused || !approx(a.Sets[0].Recoveries[0].Duration(), 90*time.Second) {
		t.Errorf("stops %+v, recoveries %+v: want the pauses as the recoveries", a.Stops, a.Sets[0].Recoveries)
	}
}

// An interval whose target steps up partway, steps down and up again, or
// surges up a short hill, is one interval, not two or three.
func TestAnIntervalWithStepsIsOne(t *testing.T) {
	var parts []part
	for i := 0; i < 4; i++ {
		parts = append(parts, work(90, 300), work(90, 345), easy(90, 150))
	}
	parts = append(parts, work(120, 320), work(35, 420), work(120, 320), easy(90, 150))
	parts = append(parts, work(60, 345), work(60, 300), work(60, 345), easy(90, 150))
	a := Analyse(build("cycling", parts...), Options{})
	if len(a.Sets) != 1 || len(a.Sets[0].Intervals) != 6 {
		t.Fatalf("sets %+v, want one of six", a.Sets)
	}
	if iv := a.Sets[0].Intervals[5]; !approx(iv.Duration(), 3*time.Minute) {
		t.Errorf("the interval that stepped down and up lasts %v, want all three steps", iv.Duration())
	}
	for i, iv := range a.Sets[0].Intervals[:4] {
		if !approx(iv.Duration(), 3*time.Minute) {
			t.Errorf("stepped interval %d lasts %v, want both its steps", i, iv.Duration())
		}
	}
	if iv := a.Sets[0].Intervals[4]; !approx(iv.Duration(), 275*time.Second) {
		t.Errorf("the interval with a surge lasts %v, want the whole of it", iv.Duration())
	}
	for _, l := range a.Levels {
		if l.Duration < time.Minute {
			t.Errorf("a level of %.0f W held for only %v: a surge is no level of its own", l.Effort, l.Duration)
		}
	}
}

// Two hard stretches are no set, and neither are stretches whose lengths
// vary as a long run's between its stops do.
func TestWhatIsNoSet(t *testing.T) {
	two := Analyse(build("running", easy(300, 150), work(600, 320), easy(120, 150), work(600, 320), easy(300, 150)), Options{})
	if len(two.Sets) != 0 {
		t.Errorf("two hard stretches made %+v", two.Sets)
	}
	ragged := Analyse(build("running", work(300, 300), easy(240, 120), work(3600, 300), easy(240, 120), work(1200, 300), easy(240, 120), work(400, 300)), Options{})
	if len(ragged.Sets) != 0 {
		t.Errorf("stretches of five minutes to an hour made %+v", ragged.Sets)
	}
}

// Climbs are found along the profile -- a dip partway up does not end
// one -- and a bump too small or too gentle is none.
func TestClimbs(t *testing.T) {
	flat := func(m float64) part { return part{secs: int(m / 3), speed: 3, power: 250} }
	up := func(m, grade float64) part { return part{secs: int(m / 3), speed: 3, power: 250, grade: grade} }
	tr := build("running",
		flat(600), up(300, 0.05), up(30, -0.03), up(150, 0.05), flat(600), // 22 m with a dip
		up(300, -0.05), flat(300),
		up(60, 0.04), flat(600), // a bump of about 2 m
		up(600, 0.01), flat(300), // 6 m at 1%
		up(300, 0.06), flat(300)) // 18 m
	a := Analyse(tr, Options{Elevation: fitactivity.ElevationOptions{Sigma: 1}})
	if len(a.Climbs) != 2 {
		t.Fatalf("%d climbs %+v, want 2", len(a.Climbs), a.Climbs)
	}
	if c := a.Climbs[0]; math.Abs(c.Gain-21) > 3 || c.Grade < 0.03 {
		t.Errorf("the first climb gains %.1f m at %.1f%%, want about 21 m over its dip", c.Gain, 100*c.Grade)
	}
	if c := a.Climbs[1]; math.Abs(c.Gain-18) > 3 {
		t.Errorf("the second climb gains %.1f m, want 18", c.Gain)
	}
	if a.Climbs[0].Start.After(a.Climbs[1].Start) || !a.Climbs[0].HasDistance {
		t.Errorf("climbs out of order or without distance: %+v", a.Climbs)
	}
}

// A climb starts where the road begins to climb, not on the gentle
// lead-in to it: 400 m rising half a per cent is the approach to a hill,
// and the climb -- and a descent, the same way down -- is the hill.
func TestAClimbStartsAtTheHill(t *testing.T) {
	flat := func(m float64) part { return part{secs: int(m / 3), speed: 3, power: 250} }
	slope := func(m, grade float64) part { return part{secs: int(m / 3), speed: 3, power: 250, grade: grade} }
	tr := build("running", flat(600), slope(400, 0.005), slope(300, 0.06), flat(600),
		slope(400, -0.005), slope(300, -0.06), flat(600))
	a := Analyse(tr, Options{Elevation: fitactivity.ElevationOptions{Sigma: 1}})
	if len(a.Climbs) != 1 || len(a.Descents) != 1 {
		t.Fatalf("climbs %+v, descents %+v; want one of each", a.Climbs, a.Descents)
	}
	if c := a.Climbs[0]; math.Abs(c.StartDistance-1000) > 30 || math.Abs(c.Gain-18) > 2 || c.Grade < 0.05 {
		t.Errorf("the climb starts at %.0f m, gaining %.1f m at %.1f%%; want the hill's 18 m at 6%% from 1000 m",
			c.StartDistance, c.Gain, 100*c.Grade)
	}
	if d := a.Descents[0]; math.Abs(d.StartDistance-2300) > 30 || d.Grade < 0.05 {
		t.Errorf("the descent starts at %.0f m at %.1f%%; want the steep way down from 2300 m", d.StartDistance, 100*d.Grade)
	}
}

// Descents are climbs the other way: a hill up and back down is a climb
// and a descent, a rise partway down does not end the descent, and a
// drop too small is none.
func TestDescents(t *testing.T) {
	flat := func(m float64) part { return part{secs: int(m / 3), speed: 3, power: 250} }
	slope := func(m, grade float64) part { return part{secs: int(m / 3), speed: 3, power: 250, grade: grade} }
	tr := build("running",
		flat(600), slope(300, 0.06), flat(300), // up 18 m
		slope(240, -0.05), slope(30, 0.03), slope(180, -0.05), flat(600), // down 21 m with a rise
		slope(60, -0.03), flat(600)) // a drop of about 2 m
	a := Analyse(tr, Options{Elevation: fitactivity.ElevationOptions{Sigma: 1}})
	if len(a.Climbs) != 1 || math.Abs(a.Climbs[0].Gain-18) > 3 {
		t.Errorf("climbs %+v, want one of 18 m", a.Climbs)
	}
	if len(a.Descents) != 1 {
		t.Fatalf("%d descents %+v, want 1", len(a.Descents), a.Descents)
	}
	d := a.Descents[0]
	if math.Abs(d.Loss-20) > 3 || d.Grade < 0.04 || d.Grade > 0.06 {
		t.Errorf("the descent loses %.1f m at %.1f%%, want about 20 m at 5%%", d.Loss, 100*d.Grade)
	}
	if dist, ok := d.Distance(); !ok || math.Abs(dist-450) > 40 {
		t.Errorf("the descent runs %.0f m, want about 450 from its top", dist)
	}
	if !d.Start.After(a.Climbs[0].End) || math.Abs(d.StartElevation-(a.Climbs[0].StartElevation+a.Climbs[0].Gain)) > 2 {
		t.Errorf("the descent %+v does not start from the climb's top", d)
	}
}

// Effort is measured by power where there is some; by grade-adjusted
// speed for a run without; by speed for a ride without; and by heart
// rate where there is nothing else.
func TestSignalChoice(t *testing.T) {
	noPower := func(sport string) *fitactivity.Track {
		return build(sport, part{secs: 600, speed: 3, grade: 0.02, noPow: true})
	}
	if a := Analyse(noPower("running"), Options{}); a.Signal != SignalGradeAdjustedSpeed {
		t.Errorf("a run without power is measured by %q", a.Signal)
	}
	if a := Analyse(noPower("cycling"), Options{}); a.Signal != SignalSpeed {
		t.Errorf("a ride without power is measured by %q", a.Signal)
	}
	hr := noPower("running")
	for i := range hr.Samples {
		hr.Samples[i].HasSpeed = false
		hr.Samples[i].HasHeartRate, hr.Samples[i].HeartRate = true, 140
	}
	if a := Analyse(hr, Options{}); a.Signal != SignalHeartRate {
		t.Errorf("heart rate alone is measured by %q", a.Signal)
	}
}

// Running uphill costs more than on the flat, so its grade-adjusted speed
// is faster than its speed; downhill a little, slower; the flat, the same.
func TestGradeAdjusted(t *testing.T) {
	if g := gradeAdjusted(3, 0); g != 3 {
		t.Errorf("on the flat %v, want 3", g)
	}
	if g := gradeAdjusted(3, 0.08); g < 4 {
		t.Errorf("up 8%% %v, want much faster than 3", g)
	}
	if g := gradeAdjusted(3, -0.05); g >= 3 {
		t.Errorf("down 5%% %v, want slower than 3", g)
	}
}

// A few seconds shuffled between two stops are no segment: every segment
// lasts at least half a minute.
func TestAScrapBetweenStopsIsNoSegment(t *testing.T) {
	stand := part{secs: 40, speed: 0, power: 0}
	shuffle := part{secs: 8, speed: 1, power: 40}
	a := Analyse(build("running", easy(600, 250), stand, shuffle, stand, easy(600, 250)), Options{})
	if len(a.Stops) != 2 {
		t.Fatalf("%d stops, want 2", len(a.Stops))
	}
	for _, s := range a.Segments {
		if s.Duration() < 30*time.Second {
			t.Errorf("a segment of %v at %.0f W", s.Duration(), s.Effort)
		}
	}
}

// A hike's brisk stretches between its stops are no set: sets are not
// looked for in a hike or a walk.
func TestNoSetsInAHike(t *testing.T) {
	var parts []part
	for i := 0; i < 6; i++ {
		parts = append(parts, part{secs: 150, speed: 1.5, grade: 0.1, noPow: true}, part{secs: 60, speed: 0, noPow: true})
	}
	hike := build("hiking", parts...)
	if a := Analyse(hike, Options{}); len(a.Sets) != 0 {
		t.Errorf("a hike has sets %+v", a.Sets)
	}
	run := build("running", parts...)
	run.Sport = "running"
	if a := Analyse(run, Options{}); len(a.Sets) != 1 {
		t.Errorf("the same stretches run, with standing recoveries, are %d sets, want 1", len(a.Sets))
	}
}
