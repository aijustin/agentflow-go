// Package ssrf provides Server-Side Request Forgery guards for URL-fetching tools.
package ssrf

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrBlocked is returned when a host or resolved address is in a blocked range.
type ErrBlocked struct {
	Host string
	IP   net.IP
}

func (e ErrBlocked) Error() string {
	if e.IP != nil {
		return fmt.Sprintf("ssrf: blocked address %s for host %q", e.IP, e.Host)
	}
	return fmt.Sprintf("ssrf: blocked host %q", e.Host)
}

// IsBlockedIP reports whether ip is in a private, link-local, CGNAT, or
// unspecified range that must not be fetched. Loopback is allowed for local
// development (matching grok-build web_fetch policy).
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		return isBlockedIPv4(ip4)
	}
	return isBlockedIPv6(ip)
}

func isBlockedIPv4(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}
	if ip.IsUnspecified() {
		return true
	}
	// 10.0.0.0/8
	if ip[0] == 10 {
		return true
	}
	// 172.16.0.0/12
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	// 169.254.0.0/16 link-local / cloud metadata
	if ip[0] == 169 && ip[1] == 254 {
		return true
	}
	// 100.64.0.0/10 CGNAT
	if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 {
		return true
	}
	return false
}

func isBlockedIPv6(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}
	if ip.IsUnspecified() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	// Unique local fc00::/7
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

// CheckURLHost validates a URL host literal IP (no DNS). Hostnames that are
// not literal IPs pass this check; callers that resolve DNS should also call
// CheckResolvedAddrs.
func CheckURLHost(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("ssrf: invalid url %q", rawURL)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("ssrf: missing host in url %q", rawURL)
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return ErrBlocked{Host: host, IP: ip}
		}
	}
	return nil
}

// CheckResolvedAddrs rejects a hostname when any resolved address is blocked.
func CheckResolvedAddrs(host string, addrs []net.IP) error {
	host = strings.TrimSpace(host)
	if len(addrs) == 0 {
		return fmt.Errorf("ssrf: no addresses resolved for host %q", host)
	}
	for _, ip := range addrs {
		if IsBlockedIP(ip) {
			return ErrBlocked{Host: host, IP: ip}
		}
	}
	return nil
}

// LookupAndCheck resolves host and rejects blocked addresses.
func LookupAndCheck(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("ssrf: empty host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return ErrBlocked{Host: host, IP: ip}
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("ssrf: dns lookup %q: %w", host, err)
	}
	return CheckResolvedAddrs(host, ips)
}
