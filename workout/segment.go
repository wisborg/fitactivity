package workout

import (
	"math"
	"sort"
	"time"

	"github.com/wisborg/fitactivity"
)

// The segmentation's tuning.
const (
	// binWidth is the time effort is averaged over before it is split:
	// a second-by-second power trace is mostly a runner's stride, and
	// five seconds average it out without blurring a change of effort.
	binWidth = 5 * time.Second
	// minBins is the fewest bins a segment is split into while being
	// found: fifteen seconds.
	minBins = 3
	// penalty scales what a split must save to be made, against the
	// noise in the effort and the length of the stretch: higher splits
	// less.
	penalty = 6.0
	// minSegment is the shortest segment kept once found: a shorter one
	// is joined to the neighbour nearest its effort. A stride or two
	// harder up a ramp is not an interval.
	minSegment = 30 * time.Second
	// stepFraction is the least two neighbouring segments' efforts
	// differ by, as a fraction of the workout's typical effort, for them
	// to stay two: nearer, they are one effort, drifting.
	stepFraction = 0.06
	// levelFraction is the gap between two efforts, as a fraction of the
	// typical effort, that makes them two levels rather than one.
	levelFraction = 0.07
	// minLevel is the least time a level holds: one held for less is a
	// moment -- a surge up a short hill in the middle of an interval --
	// and is folded into the level nearest its effort.
	minLevel = time.Minute
)

// seg is a segment while it is being found: its samples, from and to
// (exclusive), and its mean effort.
type seg struct {
	from, to int
	effort   float64
	level    int
}

// segment splits t's moving time -- what is not a stop -- into stretches
// of steady effort, groups their efforts into levels, and joins
// neighbours of one level.
//
// The split is found by penalised optimal partitioning (PELT: Killick,
// Fearnhead and Eckley, 2012) of the effort averaged over binWidth: the
// split that best explains the effort as steady stretches, each split
// paying a penalty in proportion to the effort's noise, so noise is not
// split and a real change of effort is. Then stretches shorter than
// minSegment are joined to a neighbour, and neighbours nearer than
// stepFraction are joined, since a split there is drift, not a change.
func segment(t *fitactivity.Track, effort func(int) (float64, bool), stops []Stop, power fitactivity.PowerSource) ([]Segment, []Level) {
	var segs []seg
	for _, chunk := range movingChunks(t, stops) {
		segs = append(segs, splitChunk(t, effort, chunk[0], chunk[1])...)
	}
	if len(segs) == 0 {
		return nil, nil
	}
	for i := range segs {
		segs[i].effort = mean(effort, segs[i].from, segs[i].to)
	}
	typical := typicalEffort(t, segs)
	step := stepFraction * typical
	segs = joinShort(t, effort, segs)
	segs = dropScraps(t, segs)
	segs = joinNear(t, effort, segs, step)
	levels := groupLevels(t, segs, levelFraction*typical)
	levels = regroup(t, segs, levels)
	segs = joinSameLevel(t, effort, segs)
	recount(t, segs, levels)
	out := make([]Segment, len(segs))
	for i, s := range segs {
		out[i] = describe(t, s, power)
	}
	return out, levels
}

// movingChunks are the sample ranges, [from, to), between t's stops.
func movingChunks(t *fitactivity.Track, stops []Stop) [][2]int {
	var chunks [][2]int
	i := 0
	for _, st := range stops {
		j := i
		for j < len(t.Samples) && t.Samples[j].Time.Before(st.Start) {
			j++
		}
		// The stop's first sample is its own; the chunk ends at it.
		if j > i {
			chunks = append(chunks, [2]int{i, j})
		}
		for j < len(t.Samples) && t.Samples[j].Time.Before(st.End) {
			j++
		}
		i = j
	}
	if i < len(t.Samples) {
		chunks = append(chunks, [2]int{i, len(t.Samples)})
	}
	return chunks
}

// splitChunk splits samples [from, to) of t into steady stretches.
func splitChunk(t *fitactivity.Track, effort func(int) (float64, bool), from, to int) []seg {
	// The bins: each binWidth of the chunk's time, its effort's mean,
	// and the first sample in it. A bin with no effort is left out.
	type bin struct {
		first int
		mean  float64
	}
	var bins []bin
	start := t.Samples[from].Time
	cur, sum, n := -1, 0.0, 0
	flush := func(next int) {
		if n > 0 {
			bins = append(bins, bin{first: next, mean: sum / float64(n)})
		}
	}
	first := from
	for i := from; i < to; i++ {
		b := int(t.Samples[i].Time.Sub(start) / binWidth)
		if b != cur {
			flush(first)
			cur, sum, n, first = b, 0, 0, i
		}
		if v, ok := effort(i); ok {
			sum, n = sum+v, n+1
		}
	}
	flush(first)
	if len(bins) == 0 {
		return nil
	}
	xs := make([]float64, len(bins))
	for i, b := range bins {
		xs[i] = b.mean
	}
	cuts := pelt(xs)
	var out []seg
	for k := 0; k+1 < len(cuts); k++ {
		f := bins[cuts[k]].first
		e := to
		if cuts[k+1] < len(bins) {
			e = bins[cuts[k+1]].first
		}
		if e > f {
			out = append(out, seg{from: f, to: e})
		}
	}
	return out
}

// pelt is the optimal split of xs into stretches of constant mean, as the
// indices each stretch starts at and len(xs) after the last.
func pelt(xs []float64) []int {
	n := len(xs)
	if n < 2*minBins {
		return []int{0, n}
	}
	s1 := make([]float64, n+1)
	s2 := make([]float64, n+1)
	for i, x := range xs {
		s1[i+1], s2[i+1] = s1[i]+x, s2[i]+x*x
	}
	cost := func(a, b int) float64 {
		m := float64(b - a)
		d := s1[b] - s1[a]
		return s2[b] - s2[a] - d*d/m
	}
	sigma := noise(xs)
	beta := penalty * sigma * sigma * math.Log(float64(n))
	if beta <= 0 {
		beta = 1e-9
	}
	f := make([]float64, n+1)
	last := make([]int, n+1)
	f[0] = -beta
	cands := []int{0}
	for e := minBins; e <= n; e++ {
		best, arg := math.Inf(1), 0
		for _, s := range cands {
			if e-s < minBins {
				continue
			}
			if v := f[s] + cost(s, e) + beta; v < best {
				best, arg = v, s
			}
		}
		if math.IsInf(best, 1) {
			f[e] = math.Inf(1)
			continue
		}
		f[e], last[e] = best, arg
		// Prune what can no longer be the best last split.
		kept := cands[:0]
		for _, s := range cands {
			if e-s < minBins || f[s]+cost(s, e) <= f[e] {
				kept = append(kept, s)
			}
		}
		// e is a place a later stretch may start, now its best is known.
		cands = append(kept, e)
	}
	var cuts []int
	for e := n; e > 0; e = last[e] {
		cuts = append(cuts, e)
	}
	cuts = append(cuts, 0)
	sort.Ints(cuts)
	return dedupe(cuts)
}

func dedupe(xs []int) []int {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// noise is the spread of xs from bin to bin -- the median absolute
// difference between neighbours, scaled to a standard deviation -- which
// a change of effort barely moves, being a handful of differences among
// hundreds.
func noise(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	d := make([]float64, len(xs)-1)
	for i := 1; i < len(xs); i++ {
		d[i-1] = math.Abs(xs[i] - xs[i-1])
	}
	sort.Float64s(d)
	return d[len(d)/2] / 0.6745 / math.Sqrt2
}

// mean is the mean effort of samples [from, to).
func mean(effort func(int) (float64, bool), from, to int) float64 {
	sum, n := 0.0, 0
	for i := from; i < to; i++ {
		if v, ok := effort(i); ok {
			sum, n = sum+v, n+1
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func duration(t *fitactivity.Track, s seg) time.Duration {
	end := t.Samples[s.to-1].Time
	if s.to < len(t.Samples) {
		end = t.Samples[s.to].Time
	}
	return end.Sub(t.Samples[s.from].Time)
}

// typicalEffort is the segments' effort, by time: what stepFraction and
// levelFraction are fractions of.
func typicalEffort(t *fitactivity.Track, segs []seg) float64 {
	var sum, total float64
	for _, s := range segs {
		d := duration(t, s).Seconds()
		sum, total = sum+s.effort*d, total+d
	}
	if total == 0 {
		return 0
	}
	return sum / total
}

// adjacent says segments a and b, a before b, have no stop between them.
func adjacent(a, b seg) bool { return a.to == b.from }

// joinShort joins every segment shorter than minSegment to the adjacent
// one nearer its effort, shortest first; one with no adjacent segment is
// kept, being all there was between two stops.
func joinShort(t *fitactivity.Track, effort func(int) (float64, bool), segs []seg) []seg {
	for {
		pick, shortest := -1, minSegment
		for i, s := range segs {
			if d := duration(t, s); d < shortest {
				hasNeighbour := i > 0 && adjacent(segs[i-1], s) || i+1 < len(segs) && adjacent(s, segs[i+1])
				if hasNeighbour {
					pick, shortest = i, d
				}
			}
		}
		if pick < 0 {
			return segs
		}
		into := pick - 1
		if into < 0 || !adjacent(segs[into], segs[pick]) ||
			pick+1 < len(segs) && adjacent(segs[pick], segs[pick+1]) &&
				math.Abs(segs[pick+1].effort-segs[pick].effort) < math.Abs(segs[into].effort-segs[pick].effort) {
			into = pick + 1
		}
		segs = join(effort, segs, min(into, pick))
	}
}

// dropScraps leaves out the segments shorter than minSegment that
// joinShort could not join to a neighbour, having none: a few seconds
// shuffled between two stops are not a stretch of effort, and kept as
// one, they read as a workout of their own.
func dropScraps(t *fitactivity.Track, segs []seg) []seg {
	out := segs[:0]
	for _, s := range segs {
		if duration(t, s) >= minSegment {
			out = append(out, s)
		}
	}
	return out
}

// regroup folds every level holding less than minLevel into the level
// nearest its effort, joins the neighbours that leaves of one level, and
// counts the levels again.
func regroup(t *fitactivity.Track, segs []seg, levels []Level) []Level {
	for {
		pick := -1
		for l, lv := range levels {
			if lv.Duration < minLevel && len(levels) > 1 && (pick < 0 || lv.Duration < levels[pick].Duration) {
				pick = l
			}
		}
		if pick < 0 {
			return levels
		}
		into := pick - 1
		if into < 0 || pick+1 < len(levels) && levels[pick+1].Effort-levels[pick].Effort < levels[pick].Effort-levels[into].Effort {
			into = pick + 1
		}
		for i := range segs {
			switch {
			case segs[i].level == pick:
				segs[i].level = into
			}
		}
		// Renumber past the folded level, and recount.
		for i := range segs {
			if segs[i].level > pick {
				segs[i].level--
			}
		}
		levels = append(levels[:pick], levels[pick+1:]...)
		recount(t, segs, levels)
	}
}

// recount is each level's effort and time from the segments in it.
func recount(t *fitactivity.Track, segs []seg, levels []Level) {
	sums := make([]float64, len(levels))
	for l := range levels {
		levels[l] = Level{}
	}
	for _, s := range segs {
		d := duration(t, s)
		levels[s.level].Duration += d
		sums[s.level] += s.effort * d.Seconds()
	}
	for l := range levels {
		if sec := levels[l].Duration.Seconds(); sec > 0 {
			levels[l].Effort = sums[l] / sec
		}
	}
}

// joinNear joins adjacent segments whose efforts differ by less than step,
// nearest first.
func joinNear(t *fitactivity.Track, effort func(int) (float64, bool), segs []seg, step float64) []seg {
	for {
		pick, nearest := -1, step
		for i := 0; i+1 < len(segs); i++ {
			if d := math.Abs(segs[i].effort - segs[i+1].effort); adjacent(segs[i], segs[i+1]) && d < nearest {
				pick, nearest = i, d
			}
		}
		if pick < 0 {
			return segs
		}
		segs = join(effort, segs, pick)
	}
}

// join joins segs[i] and segs[i+1].
func join(effort func(int) (float64, bool), segs []seg, i int) []seg {
	segs[i].to = segs[i+1].to
	segs[i].effort = mean(effort, segs[i].from, segs[i].to)
	return append(segs[:i+1], segs[i+2:]...)
}

// groupLevels groups the segments' efforts into levels -- sorted, a new
// level wherever two neighbouring efforts are more than gap apart -- and
// marks each segment with its level's index.
func groupLevels(t *fitactivity.Track, segs []seg, gap float64) []Level {
	order := make([]int, len(segs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return segs[order[a]].effort < segs[order[b]].effort })
	var levels []Level
	var sums []float64
	for k, i := range order {
		if k == 0 || segs[i].effort-segs[order[k-1]].effort > gap {
			levels = append(levels, Level{})
			sums = append(sums, 0)
		}
		l := len(levels) - 1
		d := duration(t, segs[i])
		levels[l].Duration += d
		sums[l] += segs[i].effort * d.Seconds()
		segs[i].level = l
	}
	for l := range levels {
		if s := levels[l].Duration.Seconds(); s > 0 {
			levels[l].Effort = sums[l] / s
		}
	}
	return levels
}

// joinSameLevel joins adjacent segments of one level: an effort that
// drifted, within its level, is still one effort.
func joinSameLevel(t *fitactivity.Track, effort func(int) (float64, bool), segs []seg) []seg {
	for i := 0; i+1 < len(segs); {
		if adjacent(segs[i], segs[i+1]) && segs[i].level == segs[i+1].level {
			segs = join(effort, segs, i)
			continue
		}
		i++
	}
	return segs
}

// describe is s as a Segment: its span and its readings' means.
func describe(t *fitactivity.Track, s seg, power fitactivity.PowerSource) Segment {
	first := t.Samples[s.from]
	end := t.Samples[s.to-1]
	if s.to < len(t.Samples) {
		end = t.Samples[s.to]
	}
	out := Segment{
		Span: Span{Start: first.Time, End: end.Time, StartDistance: first.Distance, EndDistance: end.Distance,
			HasDistance: first.HasDistance && end.HasDistance},
		Effort: s.effort,
		Level:  s.level,
	}
	var pw, sp, hr float64
	var np, ns, nh int
	for i := s.from; i < s.to; i++ {
		x := t.Samples[i]
		if p, ok := x.ResolvedPower(power); ok {
			pw, np = pw+p, np+1
		}
		if x.HasSpeed {
			sp, ns = sp+x.Speed, ns+1
		}
		if x.HasHeartRate {
			hr, nh = hr+float64(x.HeartRate), nh+1
		}
	}
	if np > 0 {
		out.Power, out.HasPower = pw/float64(np), true
	}
	if ns > 0 {
		out.Speed, out.HasSpeed = sp/float64(ns), true
	}
	if nh > 0 {
		out.HeartRate, out.HasHeartRate = hr/float64(nh), true
	}
	return out
}
