package ghwebhook

import "net"

func splitHostPort(addr string) (string, string, error) { return net.SplitHostPort(addr) }

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
