package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// A second profile lets one machine run two Lilt windows at once, each with
// its own QQ Music account: the settings, the saved login, the song cache
// and the single-instance lock are all its own, so the member account and
// the listener without one can sit in the same room for a test—or two
// people can share a computer without signing each other out.
//
//	Lilt.exe --profile guest
//
// The same name can come from LILT_PROFILE, which is easier from a script.

// profileFromArgs is the profile named on the command line, or in the
// environment, cleaned to what a directory and a lock name may hold.
func profileFromArgs(args []string, env string) string {
	name := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--profile="):
			name = strings.TrimPrefix(arg, "--profile=")
		case strings.HasPrefix(arg, "-profile="):
			name = strings.TrimPrefix(arg, "-profile=")
		case arg == "--profile" || arg == "-profile":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		}
	}
	if name == "" {
		name = env
	}
	return cleanProfileName(name)
}

// cleanProfileName keeps a profile name to letters, digits, spaces, dashes
// and underscores: it becomes a directory and part of a lock's name.
func cleanProfileName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == ' ', r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// applyProfile points this process at the named profile's directories and
// gives it a name of its own, so the single-instance lock of the main
// install does not answer for it. Without a profile it does nothing, and
// the app is the one it always was.
func applyProfile() {
	name := profileFromArgs(os.Args[1:], os.Getenv("LILT_PROFILE"))
	if name == "" {
		return
	}
	label := "Lilt " + name
	mygo.App.SetName(label)
	if base, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(base, label)
		if os.MkdirAll(dir, 0o755) == nil {
			mygo.App.SetPath(mygo.PathUserData, dir)
		}
	}
	if base, err := os.UserCacheDir(); err == nil {
		dir := filepath.Join(base, label)
		if os.MkdirAll(dir, 0o755) == nil {
			mygo.App.SetPath(mygo.PathCache, dir)
		}
	}
}
