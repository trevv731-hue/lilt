package app

import "testing"

func TestProfileFromArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want string
	}{
		{name: "flag with a value", args: []string{"--profile", "guest"}, want: "guest"},
		{name: "flag joined", args: []string{"--profile=guest"}, want: "guest"},
		{name: "short joined", args: []string{"-profile=guest"}, want: "guest"},
		{name: "environment", env: "guest", want: "guest"},
		{name: "flag wins over environment", args: []string{"--profile", "member"}, env: "guest", want: "member"},
		{name: "nothing", want: ""},
		{name: "path characters dropped", args: []string{"--profile", "../副号 2!"}, want: "副号 2"},
		{name: "a flag without a value", args: []string{"--profile"}, want: ""},
		{name: "too long", args: []string{"--profile", "abcdefghijklmnopqrstuvwxyz0123456789"}, want: "abcdefghijklmnopqrstuvwx"},
	} {
		if got := profileFromArgs(tc.args, tc.env); got != tc.want {
			t.Fatalf("%s: profile %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A profile name becomes a directory under the user's config directory, so
// it may not walk out of it.
func TestCleanProfileNameKeepsInside(t *testing.T) {
	for _, name := range []string{"..", "../..", `..\..`, "/etc", "a/b"} {
		got := cleanProfileName(name)
		if got == ".." || got == "/etc" || got == "a/b" || containsDotDot(got) {
			t.Fatalf("profile %q cleaned to %q", name, got)
		}
	}
}

func containsDotDot(name string) bool {
	for i := 0; i+1 < len(name); i++ {
		if name[i] == '.' && name[i+1] == '.' {
			return true
		}
	}
	return false
}
