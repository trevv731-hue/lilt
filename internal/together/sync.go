package together

import "time"

// How far a listener may drift before the app seeks, and how often it may
// seek again: enough to stay together, not enough to keep breaking the
// sound with corrections.
const (
	DriftLimit = 400 * time.Millisecond
	SeekEvery  = 5 * time.Second
)

// Local is what the app's own player is doing, as the sync rules see it.
type Local struct {
	TrackID  int64
	Position time.Duration
	Playing  bool
	Loading  bool
}

// Step is what the app should do to follow the room.
type Step struct {
	// Reload loads the room's track, replacing what plays.
	Reload bool
	// SetPlaying asks for playing or paused when it is not nil.
	SetPlaying *bool
	// Seek moves to Target.
	Seek   bool
	Target time.Duration
}

// Expected is where playback should be at serverNow, from a host anchor.
func Expected(pb *Playback, serverNow int64) time.Duration {
	if pb == nil || pb.Track == nil {
		return 0
	}
	at := time.Duration(pb.PositionMs) * time.Millisecond
	if pb.Playing && serverNow > pb.UpdatedAt {
		at += time.Duration(serverNow-pb.UpdatedAt) * time.Millisecond
	}
	if at < 0 {
		at = 0
	}
	if d := time.Duration(pb.Track.Duration) * time.Millisecond; d > 0 && at > d {
		at = d
	}
	return at
}

// Plan decides how a guest follows pb. A different track reloads; a
// changed play state is applied; a drift over DriftLimit seeks, at most
// once every SeekEvery seconds. While loading, the seek target is kept up
// to date so the player lands where the room is when the stream is ready.
func Plan(pb *Playback, local Local, serverNow int64, now, lastSeek time.Time) Step {
	if pb == nil || pb.Track == nil {
		return Step{}
	}
	target := Expected(pb, serverNow)
	var st Step
	if local.TrackID != pb.Track.ID {
		playing := pb.Playing
		return Step{Reload: true, SetPlaying: &playing, Seek: true, Target: target}
	}
	if local.Playing != pb.Playing {
		playing := pb.Playing
		st.SetPlaying = &playing
	}
	drift := target - local.Position
	if drift < 0 {
		drift = -drift
	}
	switch {
	case local.Loading:
		// The player keeps the seek asked for while it loads; refresh it as
		// the room moves on.
		st.Seek, st.Target = true, target
	case drift > DriftLimit && (lastSeek.IsZero() || now.Sub(lastSeek) >= SeekEvery):
		st.Seek, st.Target = true, target
	}
	return st
}
