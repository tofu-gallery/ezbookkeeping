package utils

// Tofu (2026-10-26): this file is Tofu's addition; it is not part of upstream ezBookkeeping.

import "net"

// SetInterfaceAddrsForTest replaces the machine's address list for the tests in package utils_test
// (network_test.go), and returns the function that puts the real one back
func SetInterfaceAddrsForTest(lookup func() ([]net.Addr, error)) (restore func()) {
	previous := interfaceAddrs
	interfaceAddrs = lookup

	return func() {
		interfaceAddrs = previous
	}
}
