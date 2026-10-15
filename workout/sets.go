package workout

import (
	"sort"
	"time"
)

// setSpread is how far an interval's effort may lie from its set's, as a
// fraction of the set's: fifteen per cent. A structured workout's
// intervals vary -- a hill in one, a target that steps up in the next --
// and are still one set; a surge in the cool-down, a fifth easier than
// the intervals before it, is not one of them.
const setSpread = 0.15

// minSetSize is the fewest intervals a set holds. Two hard stretches with
// a pause between are as often a run that stopped once as a workout of
// two repetitions, and are told as two hard stretches, not as a set.
const minSetSize = 3

// maxSpread is how many times its shortest interval a set's longest may
// last: a pyramid of one to four minutes is a set; a marathon's stretches
// between its drink stations, five minutes one, an hour another, are not.
const maxSpread = 5.0

// minRecovery is the least a set's recoveries last, as a fraction of its
// intervals, by their medians: a tenth. Intervals are recovered from; a
// steady run stopped for half a minute at the lights every quarter of an
// hour is not intervals, and its stops are a twentieth of what lies
// between them.
const minRecovery = 0.1

// sets finds the runs of repeated intervals among segs: the segments
// harder than what lies either side of them -- the segment before and
// after, a stop, or the activity's start or end -- taken in order, and
// grouped where one after another their efforts lie within setSpread of
// the group's. A peak far below the group's effort -- under twice
// setSpread below -- is a jog between two stops in a recovery, and is
// passed over; one nearer, or harder, ends the group. A group of one is a
// single hard stretch, not a set: a tempo block, a warm-up harder than the
// walk after it, and so is a group of two: see minSetSize. A group whose
// recoveries are shorter than minRecovery of its intervals is a steady
// effort that stopped now and then, and one whose intervals' lengths vary
// by more than maxSpread is a long effort broken by stops; neither is a
// set.
//
// Each interval then takes in the segments next to it, with no stop
// between, whose efforts lie within setSpread of the set's: an interval
// whose target stepped up partway, or that sagged up a hill, is one
// interval. What lies between two intervals is a recovery, whether it was
// jogged, walked or stood: a stop counts as no effort.
func sets(segs []Segment, stops []Stop) []Set {
	type item struct {
		start  time.Time
		seg    int // index in segs, or -1 for a stop
		effort float64
	}
	var items []item
	for i, s := range segs {
		items = append(items, item{start: s.Start, seg: i, effort: s.Effort})
	}
	for _, st := range stops {
		items = append(items, item{start: st.Start, seg: -1})
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].start.Before(items[b].start) })

	var peaks []int
	for k, it := range items {
		if it.seg < 0 {
			continue
		}
		lower := func(j int) bool { return j < 0 || j >= len(items) || items[j].effort < it.effort }
		if lower(k-1) && lower(k+1) {
			peaks = append(peaks, it.seg)
		}
	}

	var out []Set
	for i := 0; i < len(peaks); {
		group := []int{peaks[i]}
		j := i + 1
		for ; j < len(peaks); j++ {
			typical := medianEffort(segs, group)
			e := segs[peaks[j]].Effort
			if e < typical*(1-2*setSpread) {
				// A jog between two stops, in a recovery: harder than
				// standing either side of it, and no interval.
				continue
			}
			if e > typical/(1-setSpread) {
				// A surge at an interval's peak -- a short stretch, under
				// half the block it tops -- is judged by the whole block,
				// as the interval will be. A long one is its own effort.
				if be, bd := blockEffort(segs, peaks[j], typical*(1-setSpread)); 2*segs[peaks[j]].Duration() < bd {
					e = be
				}
			}
			if e < typical*(1-setSpread) || e > typical/(1-setSpread) {
				break
			}
			group = append(group, peaks[j])
		}
		i = j
		if len(group) < minSetSize {
			continue
		}
		if s, ok := makeSet(segs, group); ok {
			out = append(out, s)
		}
	}
	return out
}

// makeSet is the set whose intervals grow from the segments group, and
// whether it is one: whether its recoveries are long enough.
func makeSet(segs []Segment, group []int) (Set, bool) {
	floor := medianEffort(segs, group) * (1 - setSpread)
	var s Set
	for k, g := range group {
		first, last := g, g
		lowest := 0
		if n := len(s.Intervals); n > 0 {
			lowest = s.Intervals[n-1].Last + 1
		}
		for first > lowest && segs[first-1].End.Equal(segs[first].Start) && segs[first-1].Effort >= floor {
			first--
		}
		highest := len(segs) - 1
		if k+1 < len(group) {
			highest = group[k+1] - 1
		}
		for last < highest && segs[last].End.Equal(segs[last+1].Start) && segs[last+1].Effort >= floor {
			last++
		}
		if n := len(s.Intervals); n > 0 && s.Intervals[n-1].Last+1 == first && s.Intervals[n-1].End.Equal(segs[first].Start) {
			// Touching the interval before -- two peaks in one hard
			// block, a step down between them -- it is that interval.
			s.Intervals[n-1] = interval(segs, s.Intervals[n-1].First, last)
			continue
		}
		s.Intervals = append(s.Intervals, interval(segs, first, last))
	}
	if len(s.Intervals) < minSetSize {
		return Set{}, false
	}
	var work, rest []time.Duration
	for k := 0; k+1 < len(s.Intervals); k++ {
		a, b := s.Intervals[k], s.Intervals[k+1]
		s.Recoveries = append(s.Recoveries, Span{Start: a.End, End: b.Start,
			StartDistance: a.EndDistance, EndDistance: b.StartDistance, HasDistance: a.HasDistance && b.HasDistance})
		rest = append(rest, b.Start.Sub(a.End))
	}
	for _, iv := range s.Intervals {
		work = append(work, iv.Duration())
	}
	if medianDuration(rest).Seconds() < minRecovery*medianDuration(work).Seconds() {
		return Set{}, false
	}
	shortest, longest := work[0], work[0]
	for _, w := range work {
		shortest, longest = min(shortest, w), max(longest, w)
	}
	if longest.Seconds() > maxSpread*shortest.Seconds() {
		return Set{}, false
	}
	return s, true
}

// interval is segments first to last as one interval: their span and
// their means, by time.
func interval(segs []Segment, first, last int) Interval {
	a, b := segs[first], segs[last]
	iv := Interval{
		Span:  Span{Start: a.Start, End: b.End, StartDistance: a.StartDistance, EndDistance: b.EndDistance, HasDistance: a.HasDistance && b.HasDistance},
		First: first, Last: last,
	}
	var total, e, p, sp, hr, tp, ts, th float64
	for i := first; i <= last; i++ {
		s := segs[i]
		d := s.Duration().Seconds()
		total += d
		e += s.Effort * d
		if s.HasPower {
			p, tp = p+s.Power*d, tp+d
		}
		if s.HasSpeed {
			sp, ts = sp+s.Speed*d, ts+d
		}
		if s.HasHeartRate {
			hr, th = hr+s.HeartRate*d, th+d
		}
	}
	if total > 0 {
		iv.Effort = e / total
	}
	if tp > 0 {
		iv.Power, iv.HasPower = p/tp, true
	}
	if ts > 0 {
		iv.Speed, iv.HasSpeed = sp/ts, true
	}
	if th > 0 {
		iv.HeartRate, iv.HasHeartRate = hr/th, true
	}
	return iv
}

// blockEffort is the effort and the length of the block segs[peak] tops:
// it and the segments next to it, with no stop between, of at least
// floor -- as makeSet will grow its interval.
func blockEffort(segs []Segment, peak int, floor float64) (float64, time.Duration) {
	first, last := peak, peak
	for first > 0 && segs[first-1].End.Equal(segs[first].Start) && segs[first-1].Effort >= floor {
		first--
	}
	for last+1 < len(segs) && segs[last].End.Equal(segs[last+1].Start) && segs[last+1].Effort >= floor {
		last++
	}
	iv := interval(segs, first, last)
	return iv.Effort, iv.Duration()
}

// medianEffort is the median effort of segs[idx...].
func medianEffort(segs []Segment, idx []int) float64 {
	es := make([]float64, len(idx))
	for k, i := range idx {
		es[k] = segs[i].Effort
	}
	sort.Float64s(es)
	if len(es)%2 == 1 {
		return es[len(es)/2]
	}
	return (es[len(es)/2-1] + es[len(es)/2]) / 2
}

// medianDuration is the median of ds, and 0 for none.
func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
