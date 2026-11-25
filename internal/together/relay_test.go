package together

import "testing"

// A room carries signed play links, so a plain public address is refused;
// only this machine may be talked to in the clear.
func TestValidateRelay(t *testing.T) {
	for _, tc := range []struct {
		relay string
		ok    bool
	}{
		{"wss://together.bdprk.com", true},
		{"https://together.bdprk.com", true},
		{"ws://127.0.0.1:8787", true},
		{"http://127.0.0.1:8787", true},
		{"http://localhost:8787", true},
		{"ws://[::1]:8787", true},
		{"http://together.bdprk.com", false},
		{"ws://together.bdprk.com", false},
		{"", false},
		{"together.bdprk.com", false},
		{"ftp://together.bdprk.com", false},
	} {
		err := ValidateRelay(tc.relay)
		if tc.ok && err != nil {
			t.Fatalf("%q should be accepted: %v", tc.relay, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q should be refused", tc.relay)
		}
	}
}
