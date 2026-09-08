package fitactivity

import (
	"fmt"
	"sort"
	"time"
)

// DecodeAll decodes every path and Merges the results into a single Track,
// ordered by each file's own start time rather than by the order the paths
// were given. One path is decoded and returned unchanged, so a caller that
// accepts a variable number of activities can call this unconditionally
// instead of branching on the count.
//
// A decode failure names the file that failed and stops: a partial merge of
// the files that happened to parse would be an activity missing a stretch in
// the middle, which is far worse than no activity at all.
func DecodeAll(paths ...string) (*Track, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no activity file given")
	}
	tracks := make([]*Track, len(paths))
	for i, p := range paths {
		t, err := Decode(p)
		if err != nil {
			return nil, err
		}
		tracks[i] = t
	}
	return Merge(tracks...)
}

// Merge combines several decoded activities into one Track, as though a
// single recording had covered the whole span.
//
// This exists because a workout is not always one file. A race inside a long
// run is the usual case: the watch is stopped at the start line and a fresh
// activity is recorded for the race itself, leaving the warm-up, the race and
// the cool-down as three files that describe one afternoon. Rendering them
// separately produces three videos of a thing that happened once.
//
// # Order
//
// Tracks are ordered by their own start time -- session.start_time, or the
// first sample when the file carries no Session -- never by the order the
// caller passed them. Argument order is whatever a shell's glob produced, and
// "warmup.fit cooldown.fit race.fit" must not render a different afternoon
// than the same three files sorted by name.
//
// # Overlap is refused
//
// Two activities covering the same stretch of time -- the same file given
// twice, or two devices recording one run -- are rejected rather than merged.
// Concatenating them would count that ground twice in Sample.Distance and in
// the elevation totals, and the result would look entirely plausible: a run
// that reports 24km instead of 12km has no visible symptom in a rendered
// frame, and nothing downstream can recover the original from it. Refusing
// costs the caller one message; merging costs them a silently wrong number.
//
// Merging remains the wrong tool for that job even so. Combining two
// simultaneous recordings means reconciling two sensors' disagreeing readings
// per instant, which is a different operation with different questions
// (which device wins? per field, or per file?) and is not what this is.
//
// # Distance is rebased, and the gaps between files are not filled in
//
// Sample.Distance is cumulative from the start of ITS OWN file and restarts
// at zero in the next one, so each track's samples are offset by the total
// distance of everything before them -- otherwise the merged activity's
// distance would saw-tooth back to zero at every seam. Only samples whose
// HasDistance is set are touched; an absent distance stays absent rather than
// becoming an offset, which is the one way this function could quietly invent
// a reading that was never recorded.
//
// Ground covered BETWEEN two files -- walking from the finish line back to
// the trail -- is not recorded anywhere and is not added. The merged distance
// is the sum of what the device measured, which is the only figure the files
// support.
//
// # The gap between files becomes a pause, and that falls out
//
// Nothing here synthesises timer events. A file's closing stop pairs with the
// next file's opening start in the ordinary course of buildPauses, so the
// stretch where no watch was running resolves as a pause like any other, and
// TotalTimer -- the sum of the files' own moving time -- excludes it. The gap
// is equally a data gap, so AtWithGap already reports absent there and every
// consumer's placeholder path fires without being told this Track was merged.
//
// # What is lost
//
// Sport is the first non-empty one, so a swim-then-ride merges as a swim. The
// FIT Session carries one sport and this returns one Track, so a multi-sport
// merge necessarily flattens; a caller that needs the parts kept apart should
// keep the Tracks apart.
//
// The elevation totals and session totals are summed only when EVERY track
// carries them (HasElevationTotals, Timing.HasTotals). A sum missing one
// file's contribution is not a smaller total, it is a wrong one, and the
// consumers of these fields use them to calibrate -- BuildElevationModel
// tunes its smoothing against TotalAscent -- where a wrong figure is worse
// than the absent-data fallback it would displace.
//
// # Provenance
//
// Track.Sources lists every file that went in, in time order, and SourcePath
// is the first of them -- still one real path, so a caller naming an output
// after its input needs no special case for a merge.
//
// Samples are copied by value, but Sample.DevFields is a map and is shared
// with the input tracks rather than cloned. Nothing here writes to it; a
// caller that mutates a merged Sample's developer fields mutates the source
// track's too.
func Merge(tracks ...*Track) (*Track, error) {
	if len(tracks) == 0 {
		return nil, fmt.Errorf("no activity to merge")
	}
	for i, t := range tracks {
		if t == nil {
			return nil, fmt.Errorf("activity %d of %d is nil", i+1, len(tracks))
		}
	}
	if len(tracks) == 1 {
		// Returned as-is, not copied: one file merged is the file, and a
		// caller doing DecodeAll(single) must get exactly what Decode
		// would have handed them -- including a SourcePath that is still
		// a path, and an empty Timing.Start where the file had no Session.
		return tracks[0], nil
	}

	ordered := make([]*Track, len(tracks))
	copy(ordered, tracks)
	for i, t := range ordered {
		if len(t.Samples) == 0 {
			return nil, fmt.Errorf("%s carries no records, so there is nothing to merge", trackName(t, i))
		}
	}
	// Stable, so two files reporting the same start time keep the order the
	// caller gave them rather than an arbitrary one -- they are about to be
	// refused for overlapping anyway, and the message should name them in a
	// reproducible order.
	sort.SliceStable(ordered, func(i, j int) bool {
		return activityWindowStart(ordered[i]).Before(activityWindowStart(ordered[j]))
	})

	if err := refuseOverlap(ordered); err != nil {
		return nil, err
	}

	merged := &Track{HasElevationTotals: true}
	timing := ActivityTiming{
		// Verbatim from the earliest file, zero value included: a merge of
		// files that carry no Session must leave BuildTimerModel the same
		// fall-back to the first sample it would have had for one file.
		Start:     ordered[0].Timing.Start,
		HasTotals: true,
	}
	var distance float64
	for _, t := range ordered {
		for _, s := range t.Samples {
			if s.HasDistance {
				s.Distance += distance
			}
			merged.Samples = append(merged.Samples, s)
		}
		distance += trackDistance(t)

		timing.Events = append(timing.Events, t.Timing.Events...)
		timing.TotalTimer += t.Timing.TotalTimer
		timing.HasTotals = timing.HasTotals && t.Timing.HasTotals

		merged.TotalAscent += t.TotalAscent
		merged.TotalDescent += t.TotalDescent
		merged.HasElevationTotals = merged.HasElevationTotals && t.HasElevationTotals

		if merged.Sport == "" {
			merged.Sport = t.Sport
		}
		merged.Sources = append(merged.Sources, t.Sources...)
	}

	// Samples are NOT re-sorted. Each track arrives sorted from Decode, and
	// refuseOverlap has already established that every track's coverage
	// starts after the previous one's ends -- so concatenating them in that
	// order is sorted by construction. A sort here would not fix a violation
	// of that invariant, it would hide one.
	sort.SliceStable(timing.Events, func(i, j int) bool {
		return timing.Events[i].Time.Before(timing.Events[j].Time)
	})

	if timing.HasTotals {
		// Wall clock from the first file's start to the last file's end,
		// which counts the gaps BETWEEN the files -- they are elapsed time
		// that the merged activity spans, and they resolve as pauses.
		// TotalTimer, summed above, is the moving time and excludes them.
		_, end := BuildTimerModel(ordered[len(ordered)-1]).Window()
		timing.TotalElapsed = end.Sub(activityWindowStart(ordered[0]))
	}
	merged.Timing = timing

	// SourcePath stays ONE path -- the earliest file's -- so everything that
	// already treats it as a path (naming an output after its input, naming a
	// file in an error) keeps working on a merged Track without learning
	// anything about merging. Sources carries the rest.
	if len(merged.Sources) > 0 {
		merged.SourcePath = merged.Sources[0]
	}

	return merged, nil
}

// refuseOverlap rejects the first pair of adjacent tracks whose SAMPLES cover
// a common instant.
//
// Sample coverage is the test rather than the timer window BuildTimerModel
// resolves, which can run past the last record when a file's session totals
// include a trailing pause. It is the samples that carry the distance and the
// metrics a merge would double-count, so two files whose windows touch but
// whose records do not are a merge that works, and refusing it would reject
// ordinary consecutive recordings for the sake of a tidier rule.
//
// Touching exactly -- one file's first record at the instant of the previous
// file's last -- is refused too. That instant would appear twice in Samples,
// and a duplicate timestamp is the shape a file given twice has, not the
// shape two consecutive recordings have.
func refuseOverlap(ordered []*Track) error {
	for i := 1; i < len(ordered); i++ {
		prev, next := ordered[i-1], ordered[i]
		prevFirst, prevLast := prev.Coverage()
		nextFirst, _ := next.Coverage()
		if nextFirst.After(prevLast) {
			continue
		}
		return fmt.Errorf(
			"%s and %s overlap: %s covers %s..%s and %s starts %s. "+
				"Merging concatenates activities recorded one after another; it cannot combine "+
				"two recordings of the same stretch, which would count that distance twice",
			trackName(prev, i-1), trackName(next, i),
			trackName(prev, i-1), prevFirst.Format(time.RFC3339), prevLast.Format(time.RFC3339),
			trackName(next, i), nextFirst.Format(time.RFC3339),
		)
	}
	return nil
}

// activityWindowStart is where a Track's activity begins, resolved by
// BuildTimerModel's rules rather than by a second set written here: the
// session's start_time, or the first sample for a file carrying no Session.
// Merge orders by this and derives TotalElapsed from it, so a rule of its own
// would be free to disagree with the TimerModel every consumer then builds
// over the result.
func activityWindowStart(t *Track) time.Time {
	start, _ := BuildTimerModel(t).Window()
	return start
}

// trackDistance is how far a Track's records travelled: the largest recorded
// cumulative distance, not the last one.
//
// The two differ only on a malformed file whose final record's distance dips
// below an earlier one. Taking the maximum keeps the offset applied to every
// following track monotonic, so one glitched record cannot make the merged
// activity's distance run backwards across a seam -- which would read as the
// athlete retracing ground they had already covered.
//
// A track with no distance readings at all contributes zero, which is not an
// assertion that it covered no ground: it is the absence of any measurement
// to add. The following tracks' distances then continue from where the last
// MEASURED one left off, which is the only continuation the data supports.
func trackDistance(t *Track) float64 {
	var max float64
	for _, s := range t.Samples {
		if s.HasDistance && s.Distance > max {
			max = s.Distance
		}
	}
	return max
}

// trackName identifies a Track in an error message, falling back to its
// position for a Track built in memory rather than decoded from a file.
func trackName(t *Track, i int) string {
	if t.SourcePath != "" {
		return t.SourcePath
	}
	return fmt.Sprintf("activity %d", i+1)
}
