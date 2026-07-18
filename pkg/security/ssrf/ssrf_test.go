package ssrf

import (
	"net"
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
