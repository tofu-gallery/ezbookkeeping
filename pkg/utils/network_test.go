package utils_test

// Tofu (2026-10-26): this file is Tofu's addition; it is not part of upstream ezBookkeeping.

import (
	"errors"
	"hash/crc32"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/requestid"
	"github.com/mayswind/ezbookkeeping/pkg/settings"
	"github.com/mayswind/ezbookkeeping/pkg/utils"
)

// containerAddrs is what a container with no outside address reports: loopback and link-local addresses only
func containerAddrs() ([]net.Addr, error) {
	return []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
		&net.IPNet{IP: net.ParseIP("169.254.100.2"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
	}, nil
}

func TestGetLocalIPAddressesString_NoOtherAddress(t *testing.T) {
	defer utils.SetInterfaceAddrsForTest(containerAddrs)()

	actualValue, err := utils.GetLocalIPAddressesString()
	assert.Nil(t, err)
	assert.Equal(t, "127.0.0.1", actualValue)
}

func TestGetLocalIPAddressesString_AddressListUnavailable(t *testing.T) {
	defer utils.SetInterfaceAddrsForTest(func() ([]net.Addr, error) {
		return nil, errors.New("address list not available")
	})()

	actualValue, err := utils.GetLocalIPAddressesString()
	assert.Nil(t, err)
	assert.Equal(t, "127.0.0.1", actualValue)
}

func TestGetLocalIPAddressesString_OtherAddresses(t *testing.T) {
	defer utils.SetInterfaceAddrsForTest(func() ([]net.Addr, error) {
		addrs, _ := containerAddrs()
		addrs = append(addrs, &net.IPNet{IP: net.ParseIP("10.1.2.3"), Mask: net.CIDRMask(8, 32)})
		addrs = append(addrs, &net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)})
		return addrs, nil
	})()

	actualValue, err := utils.GetLocalIPAddressesString()
	assert.Nil(t, err)
	assert.Equal(t, "10.1.2.3,2001:db8::5", actualValue)
}

// The start that failed on Tofu: with the default HTTP address (0.0.0.0) the request ID generator asks the machine for
// its address, and a container had none to give
func TestNewDefaultRequestIdGenerator_NoOtherAddress(t *testing.T) {
	defer utils.SetInterfaceAddrsForTest(containerAddrs)()

	generator, err := requestid.NewDefaultRequestIdGenerator(core.NewNullContext(), &settings.Config{
		HttpAddr:  "0.0.0.0",
		HttpPort:  8080,
		SecretKey: "secretkey",
	})
	assert.Nil(t, err)

	expectedServerUniqId := uint16(crc32.ChecksumIEEE([]byte("127.0.0.1_secretkey")))
	actualServerUniqId := generator.GetCurrentServerUniqId()
	assert.Equal(t, expectedServerUniqId, actualServerUniqId)
}
