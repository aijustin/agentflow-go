package ssrf

import (
	"net"
	"strings"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", false},
		{"::1", false},
		{"8.8.8.8", false},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.1", true},
		{"172.32.0.1", false},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"fc00::1", true},
		{"fe80::1", true},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if got := IsBlockedIP(ip); got != tc.blocked {
				t.Fatalf("IsBlockedIP(%s)=%v want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}

func TestCheckURLHost(t *testing.T) {
	t.Parallel()
	if err := CheckURLHost("https://example.com/path"); err != nil {
		t.Fatalf("hostname should pass literal check: %v", err)
	}
	if err := CheckURLHost("http://169.254.169.254/latest"); err == nil {
		t.Fatal("expected metadata IP blocked")
	}
	if err := CheckURLHost("http://127.0.0.1:8080/"); err != nil {
		t.Fatalf("loopback should be allowed: %v", err)
	}
}

func TestErrBlockedError(t *testing.T) {
	t.Parallel()
	withIP := ErrBlocked{Host: "meta", IP: net.ParseIP("169.254.169.254")}.Error()
	if withIP == "" || !containsAll(withIP, "169.254.169.254", "meta") {
		t.Fatalf("unexpected error with IP: %q", withIP)
	}
	hostOnly := ErrBlocked{Host: "internal"}.Error()
	if hostOnly == "" || !containsAll(hostOnly, "internal") {
		t.Fatalf("unexpected host-only error: %q", hostOnly)
	}
}

func TestCheckResolvedAddrsAndLookup(t *testing.T) {
	t.Parallel()
	if err := CheckResolvedAddrs("example.com", nil); err == nil {
		t.Fatal("expected empty addr list error")
	}
	if err := CheckResolvedAddrs("example.com", []net.IP{net.ParseIP("8.8.8.8")}); err != nil {
		t.Fatalf("public addr should pass: %v", err)
	}
	if err := CheckResolvedAddrs("meta", []net.IP{net.ParseIP("169.254.169.254")}); err == nil {
		t.Fatal("expected blocked resolved addr")
	}
	if err := LookupAndCheck(""); err == nil {
		t.Fatal("expected empty host error")
	}
	if err := LookupAndCheck("127.0.0.1"); err != nil {
		t.Fatalf("loopback literal should pass: %v", err)
	}
	if err := LookupAndCheck("10.0.0.1"); err == nil {
		t.Fatal("expected private literal blocked")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if p == "" || !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
