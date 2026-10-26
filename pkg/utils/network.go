package utils

import (
	"bytes"
	"net"
)

// Tofu (2026-10-26): the machine's own address list, in a variable so that a test can replace it (network_test.go)
var interfaceAddrs = net.InterfaceAddrs

// Tofu (2026-10-26): the address GetLocalIPAddressesString gives when the machine reports no other one
const fallbackLocalIPAddress = "127.0.0.1"

// GetLocalIPAddressesString returns all local ip address, every ip split by comma
// Tofu (2026-10-26): when the machine has no address but loopback and link-local ones, or cannot list its addresses,
// this returns 127.0.0.1 instead of an error. A Tofu container has no other address, and the error stopped the server
// at start ("initializes requestid generator failed, because failed to get local address") and would skip every cron
// job run. Both callers only mix the text into an identifier and a log line.
func GetLocalIPAddressesString() (string, error) {
	localAddrs, err := GetLocalIPAddresses()

	if err != nil || len(localAddrs) < 1 {
		return fallbackLocalIPAddress, nil
	}

	buff := &bytes.Buffer{}

	for i := 0; i < len(localAddrs); i++ {
		if i > 0 {
			buff.WriteString(",")
		}

		buff.WriteString(localAddrs[i].String())
	}

	return string(buff.Bytes()), nil
}

// GetLocalIPAddresses returns all local ip address object array
func GetLocalIPAddresses() ([]net.IP, error) {
	addrs, err := interfaceAddrs()

	if err != nil {
		return nil, err
	}

	var localAddrs []net.IP

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.IsLoopback() {
				continue
			}

			if ipnet.IP.IsLinkLocalUnicast() {
				continue
			}

			ip := ipnet.IP.To16()

			if ip != nil {
				localAddrs = append(localAddrs, ip)
			}
		}
	}

	return localAddrs, nil
}
