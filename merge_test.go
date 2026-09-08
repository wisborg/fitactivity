package fitactivity

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wisborg/fitactivity/fittest"
)

// mergeFixture writes a synthetic activity under its own name, so one test can
// build several. buildFixture (decodefixture_test.go) always writes
// "activity.fit" into the temp dir, which is fine for a test decoding one file
// and useless for a test merging three.
func mergeFixture(t *testing.T, dir, name string, opts fittest.Options) *Track {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := fittest.WriteFile(path, opts); err != nil {
		t.Fatalf("building %s: %v", name, err)
	}
	track, err := Decode(path)
	if err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
	return track
}

// mergeOptions is a five-minute activity starting at start. Short, because
// these tests care about seams between files and not about duration.
func mergeOptions(start time.Time) fittest.Options {
	opts := fittest.DefaultOptions()
	opts.Start = start
	opts.Count = 300
	return opts
}

// mergeBase is an arbitrary round instant to hang the fixtures off. It is not
// a real recording's window (see fittest.DefaultOptions on why that one is the
// number it is) -- a new test should pick its own rather than reuse this.
var mergeBase = time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

// TestMerge_OrdersByStartTimeNotArgumentOrder pins the ordering rule against
// the mistake it exists to prevent: taking the caller's argument order, which
// is whatever a shell glob or a user's typing produced. The three files are
// passed deliberately shuffled, and the merged samples must still run forward
// in time.
func TestMerge_OrdersByStartTimeNotArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	warmup := mergeFixture(t, dir, "warmup.fit", mergeOptions(mergeBase))
	race := mergeFixture(t, dir, "race.fit", mergeOptions(mergeBase.Add(10*time.Minute)))
	cooldown := mergeFixture(t, dir, "cooldown.fit", mergeOptions(mergeBase.Add(20*time.Minute)))

	merged, err := Merge(cooldown, warmup, race)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got, want := len(merged.Samples), len(warmup.Samples)+len(race.Samples)+len(cooldown.Samples); got != want {
		t.Errorf("merged sample count = %d, want %d", got, want)
	}
	for i := 1; i < len(merged.Samples); i++ {
		if !merged.Samples[i].Time.After(merged.Samples[i-1].Time) {
			t.Fatalf("sample %d is at %v, not after sample %d at %v -- merged samples are not in time order",
				i, merged.Samples[i].Time, i-1, merged.Samples[i-1].Time)
		}
	}

	first, last := merged.Coverage()
	wantFirst, _ := warmup.Coverage()
	_, wantLast := cooldown.Coverage()
	if !first.Equal(wantFirst) || !last.Equal(wantLast) {
		t.Errorf("merged coverage = %v..%v, want %v..%v", first, last, wantFirst, wantLast)
	}

	// Every file named, in the resolved order rather than the argument
	// order: the provenance answers "what went into this?", so a reader
	// comparing it against a surprising total needs the sequence too.
	want := []string{warmup.SourcePath, race.SourcePath, cooldown.SourcePath}
	if !reflect.DeepEqual(merged.Sources, want) {
		t.Errorf("merged Sources = %v, want %v", merged.Sources, want)
	}
	// One real path, not a joined list: outputPath in fitdash takes
	// filepath.Base of this to name the rendered video.
	if got := merged.SourcePath; got != warmup.SourcePath {
		t.Errorf("merged SourcePath = %q, want %q (the earliest file)", got, warmup.SourcePath)
	}
}

// TestMerge_RebasesCumulativeDistance pins the one field a concatenation
// cannot leave alone. Sample.Distance restarts at zero in every file, so
// merged distance must climb monotonically across the seams and finish at the
// sum of the parts -- not saw-tooth back to zero twice, which is what a naive
// append produces and what no rendered frame would reveal.
func TestMerge_RebasesCumulativeDistance(t *testing.T) {
	dir := t.TempDir()
	warmup := mergeFixture(t, dir, "warmup.fit", mergeOptions(mergeBase))
	race := mergeFixture(t, dir, "race.fit", mergeOptions(mergeBase.Add(10*time.Minute)))

	// Stated before the merge: Merge rebases the samples of the tracks it is
	// given, so reading these afterwards would read numbers under test.
	wantTotal := trackDistance(warmup) + trackDistance(race)
	if wantTotal <= 0 {
		t.Fatalf("fixtures carry no distance (%v) -- this test would pass vacuously", wantTotal)
	}

	merged, err := Merge(warmup, race)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var prev float64
	for i, s := range merged.Samples {
		if !s.HasDistance {
			continue
		}
		if s.Distance < prev {
			t.Fatalf("sample %d distance = %v, below the previous %v -- distance ran backwards across a seam",
				i, s.Distance, prev)
		}
		prev = s.Distance
	}
	if got := trackDistance(merged); got != wantTotal {
		t.Errorf("merged total distance = %v, want %v (the sum of the two files)", got, wantTotal)
	}
}

// TestMerge_LeavesAbsentDistanceAbsent is the "absence is not zero" case for
// the rebasing above. A sample recorded with no distance must come out with no
// distance -- adding the running offset to it would manufacture a confident
// reading (an athlete 5km along) out of a field that said "unknown", and the
// presence flag that says so would still be false while the number was real.
func TestMerge_LeavesAbsentDistanceAbsent(t *testing.T) {
	first := &Track{
		SourcePath: "first.fit",
		Samples: []Sample{
			{Time: mergeBase, HasDistance: true, Distance: 0},
			{Time: mergeBase.Add(time.Second), HasDistance: true, Distance: 1000},
		},
	}
	second := &Track{
		SourcePath: "second.fit",
		Samples: []Sample{
			// No distance: a footpod that has not connected yet.
			{Time: mergeBase.Add(time.Minute), HasHeartRate: true, HeartRate: 140},
			{Time: mergeBase.Add(time.Minute + time.Second), HasDistance: true, Distance: 500},
		},
	}

	merged, err := Merge(first, second)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	absent := merged.Samples[2]
	if absent.HasDistance {
		t.Errorf("sample 2 HasDistance = true, want false -- a merge invented a distance reading")
	}
	if absent.Distance != 0 {
		t.Errorf("sample 2 Distance = %v, want 0 -- the offset was applied to an absent field", absent.Distance)
	}
	if got, want := merged.Samples[3].Distance, 1500.0; got != want {
		t.Errorf("sample 3 Distance = %v, want %v (500 rebased onto the first file's 1000)", got, want)
	}
}

// TestMerge_RefusesOverlappingActivities covers the answer this package gives
// to two recordings of the same stretch: refuse, naming both files. The
// alternative is a merged activity reporting twice the distance actually
// covered, which is not visible in any frame rendered from it and cannot be
// undone by a consumer.
func TestMerge_RefusesOverlappingActivities(t *testing.T) {
	dir := t.TempDir()
	first := mergeFixture(t, dir, "first.fit", mergeOptions(mergeBase))
	second := mergeFixture(t, dir, "second.fit", mergeOptions(mergeBase.Add(10*time.Minute)))

	cases := []struct {
		name   string
		tracks []*Track
	}{
		{
			// The commonest way to hit this: a shell glob that matched the
			// same file twice, or a copy under another name.
			name:   "the same activity given twice",
			tracks: []*Track{first, first},
		},
		{
			name: "two devices recording one run",
			tracks: []*Track{
				first,
				mergeFixture(t, dir, "watch.fit", mergeOptions(mergeBase.Add(2*time.Minute))),
			},
		},
		{
			// Refused as well: that instant would appear twice in Samples,
			// which is the shape a duplicate has and not the shape two
			// consecutive recordings have.
			name: "files touching at a single shared instant",
			tracks: func() []*Track {
				_, end := first.Coverage()
				return []*Track{first, mergeFixture(t, dir, "touching.fit", mergeOptions(end))}
			}(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			merged, err := Merge(c.tracks...)
			if err == nil {
				t.Fatalf("Merge = %v, want an error refusing the overlap", merged)
			}
			for _, want := range []string{c.tracks[0].SourcePath, c.tracks[1].SourcePath} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s -- a refusal has to say which files", err, want)
				}
			}
		})
	}

	// The control: the same check must not fire on the ordinary case it
	// shares all its arithmetic with, or the feature is refused outright.
	if _, err := Merge(first, second); err != nil {
		t.Errorf("Merge of two consecutive activities = %v, want no error", err)
	}
}

// TestMerge_GapBetweenFilesResolvesAsAPause pins the property that makes this
// feature cheap: nothing synthesises timer events, and the stretch where no
// watch was running still comes out as a pause because the first file's
// closing stop pairs with the next file's opening start in buildPauses.
// Elapsed spans the gap; Active does not.
func TestMerge_GapBetweenFilesResolvesAsAPause(t *testing.T) {
	const (
		fileSpan = 299 * time.Second // Count-1, per fittest.Options.Count
		gap      = 10 * time.Minute
	)
	dir := t.TempDir()
	first := mergeFixture(t, dir, "first.fit", mergeOptions(mergeBase))
	second := mergeFixture(t, dir, "second.fit", mergeOptions(mergeBase.Add(fileSpan+gap)))

	merged, err := Merge(first, second)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	model := BuildTimerModel(merged)

	start, end := model.Window()
	if !start.Equal(mergeBase) {
		t.Errorf("window start = %v, want %v", start, mergeBase)
	}
	if want := mergeBase.Add(2*fileSpan + gap); !end.Equal(want) {
		t.Errorf("window end = %v, want %v", end, want)
	}

	pauses := model.Pauses()
	if len(pauses) != 1 {
		t.Fatalf("Pauses() = %v, want exactly the one gap between the files", pauses)
	}
	if want := mergeBase.Add(fileSpan); !pauses[0].Start.Equal(want) {
		t.Errorf("pause starts %v, want %v (where the first file stopped)", pauses[0].Start, want)
	}
	if want := mergeBase.Add(fileSpan + gap); !pauses[0].End.Equal(want) {
		t.Errorf("pause ends %v, want %v (where the second file started)", pauses[0].End, want)
	}

	if got, want := model.Elapsed(end), 2*fileSpan+gap; got != want {
		t.Errorf("Elapsed at end = %v, want %v (the gap is elapsed time)", got, want)
	}
	if got, want := model.Active(end), 2*fileSpan; got != want {
		t.Errorf("Active at end = %v, want %v (the gap is not moving time)", got, want)
	}
}

// TestMerge_TotalsNeedEveryTrack pins the all-or-nothing rule for the summed
// session figures. A total missing one file's contribution is not a smaller
// number, it is a wrong one -- and BuildElevationModel calibrates its
// smoothing against TotalAscent, so a wrong figure there displaces the
// absent-data fallback with something worse than nothing.
func TestMerge_TotalsNeedEveryTrack(t *testing.T) {
	full := func(path string, start time.Time) *Track {
		return &Track{
			SourcePath:         path,
			Samples:            []Sample{{Time: start}, {Time: start.Add(time.Minute)}},
			TotalAscent:        100,
			TotalDescent:       80,
			HasElevationTotals: true,
			Timing: ActivityTiming{
				Start: start, TotalElapsed: time.Minute, TotalTimer: time.Minute, HasTotals: true,
			},
		}
	}

	t.Run("summed when every track carries them", func(t *testing.T) {
		merged, err := Merge(full("a.fit", mergeBase), full("b.fit", mergeBase.Add(time.Hour)))
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if !merged.HasElevationTotals || merged.TotalAscent != 200 || merged.TotalDescent != 160 {
			t.Errorf("elevation totals = %v/%v (has=%v), want 200/160 (has=true)",
				merged.TotalAscent, merged.TotalDescent, merged.HasElevationTotals)
		}
		if !merged.Timing.HasTotals {
			t.Fatal("Timing.HasTotals = false, want true")
		}
		if got, want := merged.Timing.TotalTimer, 2*time.Minute; got != want {
			t.Errorf("TotalTimer = %v, want %v (the sum of the files' moving time)", got, want)
		}
		if got, want := merged.Timing.TotalElapsed, time.Hour+time.Minute; got != want {
			t.Errorf("TotalElapsed = %v, want %v (first start to last end, gap included)", got, want)
		}
	})

	t.Run("withheld when one track lacks them", func(t *testing.T) {
		partial := full("b.fit", mergeBase.Add(time.Hour))
		partial.HasElevationTotals = false
		partial.TotalAscent, partial.TotalDescent = 0, 0
		partial.Timing.HasTotals = false

		merged, err := Merge(full("a.fit", mergeBase), partial)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if merged.HasElevationTotals {
			t.Error("HasElevationTotals = true, want false -- one file's totals are not the activity's")
		}
		if merged.Timing.HasTotals {
			t.Error("Timing.HasTotals = true, want false")
		}
		if merged.Timing.TotalElapsed != 0 {
			t.Errorf("TotalElapsed = %v, want 0 -- an unusable total must not be published as a number",
				merged.Timing.TotalElapsed)
		}
	})
}

// TestMerge_SportIsTheFirstNonEmptyOne documents the flattening a multi-sport
// merge accepts: one Track carries one Sport, so a swim followed by a ride
// reports the swim. It is pinned rather than left to be discovered from a
// rendered video captioned with the wrong activity type.
func TestMerge_SportIsTheFirstNonEmptyOne(t *testing.T) {
	unknown := &Track{SourcePath: "a.fit", Samples: []Sample{{Time: mergeBase}}}
	ride := &Track{
		SourcePath: "b.fit",
		Sport:      "cycling",
		Samples:    []Sample{{Time: mergeBase.Add(time.Hour)}},
	}
	run := &Track{
		SourcePath: "c.fit",
		Sport:      "running",
		Samples:    []Sample{{Time: mergeBase.Add(2 * time.Hour)}},
	}

	merged, err := Merge(unknown, ride, run)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got, want := merged.Sport, "cycling"; got != want {
		t.Errorf("Sport = %q, want %q -- the first file naming one wins, not the first file", got, want)
	}
}

// TestMerge_SingleTrackIsReturnedUnchanged pins the identity case DecodeAll
// leans on: a caller handed one file must get exactly what Decode returns,
// so accepting a variable number of activities costs nothing when there is
// one. In particular SourcePath stays a path rather than becoming a
// one-element joined list.
func TestMerge_SingleTrackIsReturnedUnchanged(t *testing.T) {
	track := mergeFixture(t, t.TempDir(), "only.fit", mergeOptions(mergeBase))

	merged, err := Merge(track)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged != track {
		t.Fatalf("Merge(one) returned a different Track, want the same one unchanged")
	}
}

// TestMerge_RefusesUnusableInput covers the arguments that cannot produce an
// activity at all. An empty track is refused rather than skipped: silently
// dropping it would render an activity with a hole where the caller believed
// a file was, and say so nowhere.
func TestMerge_RefusesUnusableInput(t *testing.T) {
	ok := &Track{SourcePath: "ok.fit", Samples: []Sample{{Time: mergeBase}}}

	cases := []struct {
		name   string
		tracks []*Track
		want   string
	}{
		{"no tracks at all", nil, "no activity to merge"},
		{"a nil track", []*Track{ok, nil}, "activity 2 of 2 is nil"},
		{"a track with no records", []*Track{ok, {SourcePath: "empty.fit"}}, "empty.fit carries no records"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Merge(c.tracks...)
			if err == nil {
				t.Fatalf("Merge = nil error, want %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// TestDecodeAll_DecodesAndMerges covers the convenience wrapper end to end,
// including the failure that matters most: one unreadable file stops the
// whole thing rather than yielding a merge of whatever else parsed, which
// would be an activity missing a stretch in the middle with nothing saying so.
func TestDecodeAll_DecodesAndMerges(t *testing.T) {
	dir := t.TempDir()
	first := mergeFixture(t, dir, "first.fit", mergeOptions(mergeBase))
	second := mergeFixture(t, dir, "second.fit", mergeOptions(mergeBase.Add(10*time.Minute)))

	merged, err := DecodeAll(second.SourcePath, first.SourcePath)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if got, want := len(merged.Samples), len(first.Samples)+len(second.Samples); got != want {
		t.Errorf("merged sample count = %d, want %d", got, want)
	}
	if first, _ := merged.Coverage(); !first.Equal(mergeBase) {
		t.Errorf("merged coverage starts %v, want %v -- DecodeAll must order by start time too", first, mergeBase)
	}

	t.Run("one unreadable file stops the merge", func(t *testing.T) {
		missing := filepath.Join(dir, "nope.fit")
		if _, err := DecodeAll(first.SourcePath, missing); err == nil {
			t.Fatal("DecodeAll = nil error, want a failure naming the unreadable file")
		} else if !strings.Contains(err.Error(), "nope.fit") {
			t.Errorf("error = %q, want it to name %s", err, missing)
		}
	})

	t.Run("no paths at all", func(t *testing.T) {
		if _, err := DecodeAll(); err == nil {
			t.Fatal("DecodeAll() = nil error, want a refusal")
		}
	})
}

// TestDecode_RecordsItsOwnSourceInSources pins the invariant Merge and every
// consumer of Sources depend on: a decoded Track already has a one-element
// list, so nothing needs to special-case "this Track was never merged".
func TestDecode_RecordsItsOwnSourceInSources(t *testing.T) {
	track := mergeFixture(t, t.TempDir(), "only.fit", mergeOptions(mergeBase))

	if want := []string{track.SourcePath}; !reflect.DeepEqual(track.Sources, want) {
		t.Errorf("Sources = %v, want %v", track.Sources, want)
	}
}
