package together

import (
	"testing"
	"time"
)

func playback(id int64, playing bool, pos int64, updated int64, duration int64) *Playback {
	return &Playback{
		Track:      &Track{ID: id, Duration: duration},
		Playing:    playing,
		PositionMs: pos,
		UpdatedAt:  updated,
	}
}

func TestExpectedAdvancesWhilePlaying(t *testing.T) {
	pb := playback(1, true, 10_000, 1_000, 200_000)
	if got := Expected(pb, 3_500); got != 12_500*time.Millisecond {
		t.Fatalf("expected 12.5s, got %v", got)
	}
}

func TestExpectedHoldsWhilePaused(t *testing.T) {
	pb := playback(1, false, 10_000, 1_000, 200_000)
	if got := Expected(pb, 60_000); got != 10*time.Second {
		t.Fatalf("expected 10s, got %v", got)
	}
}

func TestExpectedClampsToDuration(t *testing.T) {
	pb := playback(1, true, 100_000, 1_000, 100_000)
	if got := Expected(pb, 500_000); got != 100*time.Second {
		t.Fatalf("expected the duration, got %v", got)
	}
}

func TestPlanReloadsAnotherTrack(t *testing.T) {
	pb := playback(2, true, 5_000, 1_000, 200_000)
	st := Plan(pb, Local{TrackID: 1, Position: 90 * time.Second, Playing: true}, 6_000, time.Now(), time.Now())
	if !st.Reload || !st.Seek || st.Target != 10*time.Second {
		t.Fatalf("unexpected step: %+v", st)
	}
	if st.SetPlaying == nil || !*st.SetPlaying {
		t.Fatalf("reload should keep playing: %+v", st)
	}
}

func TestPlanAppliesPause(t *testing.T) {
	pb := playback(1, false, 5_000, 1_000, 200_000)
	now := time.Now()
	st := Plan(pb, Local{TrackID: 1, Position: 5 * time.Second, Playing: true}, 10_000, now, now)
	if st.Reload || st.Seek {
		t.Fatalf("no seek needed: %+v", st)
	}
	if st.SetPlaying == nil || *st.SetPlaying {
		t.Fatalf("should ask to pause: %+v", st)
	}
}

func TestPlanIgnoresSmallDrift(t *testing.T) {
	pb := playback(1, true, 5_000, 1_000, 200_000)
	now := time.Now()
	st := Plan(pb, Local{TrackID: 1, Position: 5*time.Second + 200*time.Millisecond, Playing: true}, 1_000, now, now)
	if st.Seek || st.SetPlaying != nil {
		t.Fatalf("200ms is inside the limit: %+v", st)
	}
}

func TestPlanSeeksLargeDriftAfterCooldown(t *testing.T) {
	pb := playback(1, true, 5_000, 1_000, 200_000)
	now := time.Now()
	st := Plan(pb, Local{TrackID: 1, Position: 9 * time.Second, Playing: true}, 1_000, now, now.Add(-SeekEvery))
	if !st.Seek {
		t.Fatalf("4s drift should seek: %+v", st)
	}
	if st.Target != 5*time.Second {
		t.Fatalf("target should be the room's 5s, got %v", st.Target)
	}
}

func TestPlanWaitsForSeekCooldown(t *testing.T) {
	pb := playback(1, true, 5_000, 1_000, 200_000)
	now := time.Now()
	st := Plan(pb, Local{TrackID: 1, Position: 9 * time.Second, Playing: true}, 1_000, now, now.Add(-time.Second))
	if st.Seek {
		t.Fatalf("a seek a second ago should not repeat: %+v", st)
	}
}

func TestPlanKeepsLoadingSeekFresh(t *testing.T) {
	pb := playback(1, true, 5_000, 1_000, 200_000)
	now := time.Now()
	st := Plan(pb, Local{TrackID: 1, Loading: true, Playing: true, Position: 0}, 9_000, now, now)
	if !st.Seek || st.Target != 13*time.Second {
		t.Fatalf("loading should be told where to land: %+v", st)
	}
}
