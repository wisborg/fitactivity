# fitactivity

Go package for reading a Garmin FIT activity file and deriving the models a
program draws or exports from: an interpolatable track, an elevation profile,
kilometre splits, and an elapsed-versus-active timer.

```go
import "github.com/wisborg/fitactivity"

track, err := fitactivity.Decode("activity.fit")
if err != nil {
    return err
}

// The track is per-second; ask it for any instant in between.
s, ok := track.At(t)
if ok && s.HasHeartRate {
    fmt.Printf("%d bpm\n", s.HeartRate)
}
```

It exists because two programs needed the same decoder — [videofx][videofx],
which pairs an activity with a video clip recorded alongside it, and fitdash,
which renders an activity on its own into a dashboard video — and the FIT
sentinel handling is the last code either of them should own two copies of.

[videofx]: https://github.com/wisborg/videofx

## Absence is not zero

FIT marks an absent field with a type-specific sentinel rather than zero. A GPS
fix that has not happened yet — before satellite lock, or lost under tree cover
— is not the same datum as a fix at 0,0 off the coast of Ghana, and a decoder
that returns the sentinel as a value corrupts everything downstream silently
instead of loudly.

So every field that can be absent carries an explicit presence flag:

```go
if s.HasGPS {
    // Lat/Lon are meaningless when this is false.
}
```

Developer fields (a Stryd footpod's running dynamics, say) use the same rule
expressed as map membership: a field whose raw value was its base type's
invalid sentinel is omitted from `Sample.DevFields` rather than stored as a
bogus zero.

This is the property the package spends most of its complexity on, and it only
pays off if you preserve it. A gauge that renders a missing heart rate as
"0 bpm" has thrown the distinction away again.

## What it gives you

| | |
|---|---|
| `Decode` | FIT file → `*Track`: sorted `Sample`s, sport, session totals, timer events |
| `Track.At` / `AtWithGap` | the track interpolated at an arbitrary instant, with per-field presence carried through and a max-gap that refuses to invent data across a dropout |
| `Track.Window` / `Resample` | a stretch of the track, raw or on a fixed step |
| `BuildElevationModel` | smoothed elevation with its own distance axis, cumulative gain/loss, and grade — tuned to the session's own ascent/descent totals where the file reports them |
| `BuildSplits` | kilometre boundaries, per-lap durations, fastest lap |
| `BuildTimerModel` | elapsed vs. active time, from the file's `timer` start/stop events |
| `Resolve`, `BuildClipPoints`, `Scope` | pairing an activity with a video clip recorded during it — camera-clock vs. watch-clock re-basing and clip scoping |
| `WriteGPX`, `WriteSRT` | GPX 1.1 and subtitle sidecars |
| `fittest` | generates synthetic FIT activities, so tests need no real recording |

The last three rows serve videofx specifically; a consumer with no video in the
picture can ignore them.

## Testing without personal data

`fittest` writes a synthetic activity — a straight-line course at a constant
pace, with a heart rate and elevation that vary smoothly and mean nothing — so
no test in this repository needs a real recording, and none is committed here.

```go
path := filepath.Join(t.TempDir(), "activity.fit")
if err := fittest.WriteFile(path, fittest.DefaultOptions()); err != nil {
    t.Fatal(err)
}
track, err := fitactivity.Decode(path)
```

It is generated rather than checked in on purpose: a binary blob in a repo is
unreviewable, and nobody can tell from a diff that it holds no personal data.

One test does need a real device file, because a file this package wrote cannot
prove anything about the ones Garmin writes. It is gated on an environment
variable pointing outside the repo and skips without it:

```
FITACTIVITY_REAL_FIT="$HOME/activities/run.fit" go test ./...
```

Its expectations pin one specific recording, so pointing it at a different
activity fails rather than passes. That is deliberate: it is a regression pin,
not a "can we read any FIT?" check.

## License

Apache-2.0. See `LICENSE`, and `NOTICE` for third-party attribution.
