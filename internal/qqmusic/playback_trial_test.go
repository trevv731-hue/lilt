package qqmusic

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A trial is what QQ serves to an account without the rights a track needs:
// the API still reports the song's full size, so only the file's own length
// tells them apart.
func TestTrialLike(t *testing.T) {
	vip := Track{ID: 1, Duration: 240_000, Fee: 1, Size128: 1_000_000, Size320: 2_000_000}
	free := Track{ID: 2, Duration: 240_000, Fee: 0, Size128: 1_000_000}
	long := Stream{URL: "https://x/long.mp3", Size: 1_000_000, Level: "standard"}
	short := Stream{URL: "https://x/short.mp3", Size: 125_000, Level: "standard"}

	if !vip.TrialLike(short) {
		t.Fatal("thirty seconds of a four-minute song is a trial")
	}
	if vip.TrialLike(long) {
		t.Fatal("the full song is not a trial")
	}
	if free.TrialLike(short) {
		t.Fatal("a free song is never a trial")
	}
	if (Track{Fee: 1}).TrialLike(short) {
		t.Fatal("without a known size, nothing can be called a trial")
	}
}

// The size a trial is judged against follows the requested quality, and a
// track whose sizes are missing falls back to its duration.
func TestExpectedBytes(t *testing.T) {
	tr := Track{Duration: 200_000, Size128: 3_200_000, SizeFLAC: 9_000_000}
	if got := tr.ExpectedBytes("standard"); got != 3_200_000 {
		t.Fatalf("standard expected %d", got)
	}
	if got := tr.ExpectedBytes("exhigh"); got != 3_200_000 {
		t.Fatalf("exhigh without a 320 size should use the 128 one, got %d", got)
	}
	if got := tr.ExpectedBytes("lossless"); got != 9_000_000 {
		t.Fatalf("lossless expected %d", got)
	}
	if got := (Track{Duration: 100_000}).ExpectedBytes("standard"); got != 1_600_000 {
		t.Fatalf("a size QQ did not report should come from the duration, got %d", got)
	}
	if got := (Track{}).ExpectedBytes("standard"); got != 0 {
		t.Fatalf("nothing known expected 0, got %d", got)
	}
}

// The CDN's answer to a range request is what says how large the file is.
func TestProbeStreamSize(t *testing.T) {
	file := []byte("not really an mp3, but enough bytes to measure")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/range":
			if r.Header.Get("Range") == "" {
				t.Errorf("no range asked for")
			}
			w.Header().Set("Content-Range", "bytes 0-1/12345")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(file[:2])
		case "/whole":
			_, _ = w.Write(file)
		case "/partial-range":
			w.Header().Set("Content-Range", "bytes 0-1/*")
			w.WriteHeader(http.StatusPartialContent)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	if got := ProbeStreamSize(srv.URL + "/range"); got != 12345 {
		t.Fatalf("ranged size %d", got)
	}
	if got := ProbeStreamSize(srv.URL + "/whole"); got != int64(len(file)) {
		t.Fatalf("whole size %d, want %d", got, len(file))
	}
	if got := ProbeStreamSize(srv.URL + "/partial-range"); got != 0 {
		t.Fatalf("an unknown total size %d", got)
	}
	if got := ProbeStreamSize(srv.URL + "/forbidden"); got != 0 {
		t.Fatalf("a refused probe size %d", got)
	}
}

func TestContentRangeTotal(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{"bytes 0-1/12345", 12345},
		{"bytes 0-1/*", 0},
		{"", 0},
		{"nonsense", 0},
	} {
		if got := contentRangeTotal(tc.value); got != tc.want {
			t.Fatalf("contentRangeTotal(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}
