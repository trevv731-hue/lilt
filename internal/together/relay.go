package together

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateRelay accepts a room server address: an encrypted one anywhere,
// or a plain one on this machine for development. A room carries the links
// QQ Music signed for its members, and a plain connection would put them on
// the network for anyone to read, so a public plain address is refused
// rather than quietly used.
func ValidateRelay(relay string) error {
	raw := strings.TrimSpace(relay)
	if raw == "" {
		return errors.New("房间服务器地址为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("房间服务器地址无效")
	}
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		return nil
	case "ws", "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return errors.New("房间服务器要用 wss://；明文只允许本机调试")
	default:
		return errors.New("房间服务器地址需要是 https、wss（或本机的 http、ws）")
	}
}

// isLoopback reports whether host is this machine, where a plain connection
// never leaves it.
func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}
