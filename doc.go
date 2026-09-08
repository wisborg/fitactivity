// Package fitactivity decodes a Garmin FIT activity file
// (github.com/muktihari/fit) into a Track -- a time-sorted, in-memory slice
// of per-second Samples covering GPS, speed, distance, heart rate, cadence,
// temperature, power, and Stryd running-dynamics developer fields -- and
// derives the models a consumer actually draws or exports from.
//
// It is the shared data layer beneath the projects that read a recorded
// activity: videofx, which pairs one against a video clip it was recorded
// alongside, and fitdash, which renders one on its own into a dashboard
// video. Nothing here knows about either. This package produces the
// activity's data and its derived models; deciding what a frame of video
// looks like is entirely the caller's business.
//
// # What is here
//
// Decode (decode.go) reads the file, and Merge/DecodeAll (merge.go) combine
// several into one Track for a workout split across files -- a race recorded
// separately inside a long run -- rebasing the per-file cumulative distance
// and refusing two recordings of the same stretch. Track.At/AtWithGap (sync.go)
// interpolates the Track at an arbitrary instant, with explicit per-field
// presence propagation and a configurable max-gap so a data dropout is never
// papered over with a fabricated straight line; Track.Window/Resample slice
// or re-sample a stretch of it. Three derived models sit on top: an
// ElevationModel (elevation.go) that smooths a barometric elevation trace and
// carries its own distance axis, cumulative gain/loss and grade; Splits
// (splits.go), the kilometre boundaries and lap times; and a TimerModel
// (timer.go), which turns the file's `timer` events into elapsed-versus-active
// time so a paused activity reports both honestly.
//
// A second group exists for pairing an activity with a video clip recorded
// during it, and is used by videofx rather than by every consumer: Resolve
// (sync.go) maps a clip's container creation_time plus a user clock-skew
// offset onto a Track's coverage, BuildClipPoints (points.go) re-bases the
// result onto the clip's own camera clock, Scope (scope.go) narrows an
// activity to the stretch a clip covers, and WriteGPX/WriteSRT (gpx.go,
// srt.go) emit the sidecars a downstream overlay tool reads. A consumer with
// no video in the picture can ignore all of it.
//
// # Absence is not zero
//
// FIT's wire format marks an absent field with a type-specific sentinel
// (e.g. 0xFFFF for a uint16, a specific all-ones bit pattern for a float64)
// rather than zero -- a GPS fix that hasn't happened yet (before satellite
// lock, or lost under tree/tunnel cover) is not the same datum as a fix at
// 0,0 off the coast of Ghana, and treating the sentinel as a real value would
// corrupt every downstream computation silently rather than loudly. Every
// Sample field that can be absent is therefore modeled with an explicit
// boolean presence flag (or, for developer fields, by the key simply being
// absent from the map) -- never a magic zero a caller might mistake for real
// data.
//
// This is the single most load-bearing property of the package, and it only
// pays off if consumers preserve it. A panel that renders a missing heart rate
// as "0 bpm", or an exporter that writes a 0,0 trackpoint, has thrown away the
// distinction this package spends its complexity maintaining.
//
// # Error prefixes
//
// Errors from this package are deliberately NOT prefixed with a package name,
// unlike much Go code. Every message here already names the file it is about,
// and both known consumers wrap what they get with their own layer's
// attribution -- so a prefix here produced doubled, uninformative chains like
// "telemetry: telemetry: opening x.fit: ...". Wrap at the boundary that has
// context the user can act on, which is never this one.
package fitactivity
