# fitactivity

Go package for reading a recorded activity — FIT, GPX, TCX, KML, KMZ, a Flightradar24 flight's CSV or a
Strava activity's JSON — and
deriving the models a program draws or exports from: an interpolatable track, an elevation profile,
kilometre splits, and an elapsed-versus-active timer.

```go
import "github.com/wisborg/fitactivity"

track, err := fitactivity.Read("activity.fit") // or .gpx, .tcx, .kml, .kmz
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
| `Read` | FIT, GPX, TCX, KML, KMZ, a Flightradar24 CSV or a Strava activity's JSON with its streams → `*Track`, told apart by content; a file without a time on every point is refused with `ErrNoTimes` rather than given invented ones |
| `Track.Flights`, `Sample.Course` | a flight tracker's flight — number, callsign, the airports it flew between — and the direction of travel it reports; absent for anything else. A Flightradar24 KML alone is the whole flight: the same reports as its CSV, speed and track included, read from each point's pop-up, plus the flight number and airports the CSV lacks |
| `Track.Title`, `Description`, `Strava` | what the activity was called and what was written about it, and the activity on Strava — its id and page, the segments Strava matched along it, its Relative Effort (the API's `suffer_score`), workout type, commute and trainer flags, perceived exertion and gear, Strava's calories and kilojoules, its photo, kudos and comments — for one read from Strava's JSON (RunGap's `.strava.json` export, or what the API answers with the streams beside it). Only what was recorded becomes samples: Strava's smoothed speed, grade and moving flags are its own workings and are left out, and what a FIT file has that Strava drops — developer fields, running dynamics, timer events — is absent |
| `ParseStrava`, `Track.AttachStrava` | Strava's details of an activity, without its streams, given to a track read from elsewhere: a watch's FIT file with Strava's title, description and segments |
| `strava` | a user's own activities fetched from Strava's API with their own registered application: `Login` (a browser sent back to localhost, or an address pasted), `Activities` over a date range, `Match` to a recording's start, `Track` with the streams, `Attach` of Strava's details to a FIT file's track, `AddGear` of the gear's brand, model and description, asked for only when wanted. Strava's API Policy is kept by the package rather than its callers: answers are cached a day and then deleted, only the athlete's own data is fetched, and a rate limit reached is reported with when it resets. See [Strava](#strava) |
| `ReadRoute` | any of those → `*Route`, the geometry alone: what a planned route has, and what a map needs |
| `Track.At` / `AtWithGap` | the track interpolated at an arbitrary instant, with per-field presence carried through and a max-gap that refuses to invent data across a dropout |
| `Track.GapTolerance` | the max-gap the track's own sampling calls for: a second's recording keeps the default, a flight tracker reporting every half-minute gets twice that; `Track.MaxGap`, when set, answers instead — a program drawing a flight across a hole in its tracker's coverage sets it |
| `Track.FillDistanceFromGPS` | a distance summed along the fixes, asked for by name, for a file that records none — `Read` itself never invents one |
| `Track.Window` / `Resample` | a stretch of the track, raw or on a fixed step |
| `BuildElevationModel` | smoothed elevation with its own distance axis, cumulative gain/loss, and grade — tuned to the session's own ascent/descent totals where the file reports them |
| `BuildSplits` | kilometre boundaries, per-lap durations, fastest lap |
| `BuildTimerModel` | elapsed vs. active time, from the file's `timer` start/stop events |
| `Resolve`, `BuildClipPoints`, `Scope` | pairing an activity with a video clip recorded during it — camera-clock vs. watch-clock re-basing and clip scoping |
| `WriteGPX`, `WriteSRT` | GPX 1.1 and subtitle sidecars |
| `fittest` | generates synthetic FIT activities, so tests need no real recording |
| `units` | the units numbers are shown in — metric, imperial, or one by one (a flight's feet, nautical miles and knots) — with conversions from SI (and °C to °F) and pace and speed formatting |

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

## Strava

`strava` reads a user's own activities from Strava's API. Each user registers their own API
application at <https://www.strava.com/settings/api>, with `localhost` as its Authorization
Callback Domain, and gives its client id and secret in `~/.config/strava.yaml`:

```yaml
strava:
  client_id: 12345
  client_secret: 0123abcd...
```

or in `$STRAVA_CLIENT_ID` and `$STRAVA_CLIENT_SECRET`, in a file `$STRAVA_CONFIG` names, or in the
system's keychain as `strava/client_id` and `strava/client_secret`. Nothing is built in: a secret
shipped in a program is nobody's secret, and an application's rate limits and athlete capacity are
its own. The login's token is kept in the system's keychain -- the macOS Keychain, the Windows
Credential Manager, a Secret Service on Linux -- where one answers, and beside the credentials in
`strava-token.json`, for its owner alone, where not; a token in that file from before is moved into
the keychain at its first use. It is refreshed under a lock so two programs do not spend the same
refresh token. All of these are read and kept through
[secrets](https://github.com/wisborg/secrets), whose `$SECRETS_STORE=file` keeps the keychain out
of it.

What Strava's [API Policy](https://www.strava.com/legal/api_policy) asks is kept here: answers are
cached for a day under the system's cache directory and deleted once older, and `Client.Refresh`
asks again for what is fetched -- an activity edited on Strava since -- keeping the new answers; only the athlete's
own activities are fetched; a rate limit reached is reported, never retried in a loop. The Policy
also forbids using Strava data in operating an AI application, putting it into one's context
included, so what this package fetches is not for a language model to read.

## Weather

`weather` is the weather an activity was done in, from [Open-Meteo](https://open-meteo.com/)'s
archive: temperature, dew point, humidity, wind and gusts, surface pressure and sunlight, hourly,
at a few places along the route -- one every 10 km -- in one request, and at any sample by
interpolating between the hours. `Headings` gives each sample its direction of travel and
`Relative` the wind as the athlete met it, head and cross.

It is a model's weather: ECMWF's IFS at about 9 km for recent days, the ERA5 reanalysis at about
25 km once it has caught up five days on, with the wind at 10 m in the open. Fetching sends the
activity's area and hours to Open-Meteo, so a program does it only when its user asks; answers
are kept under the system's cache directory, for good once the archive has settled. The data are
CC BY 4.0, and `weather.Credit` belongs beside wherever they are shown. The free API is for
non-commercial use.

## License

Apache-2.0. See `LICENSE`, and `NOTICE` for third-party attribution.
