package egress

import (
	"net/netip"
	"testing"
)

func TestPublic(t *testing.T) {
	for _, row := range []struct {
		addr   string
		public bool
		what   string
	}{
		{"8.8.8.8", true, "a public resolver"},
		{"93.184.215.14", true, "a public site"},
		{"192.31.196.1", true, "AS112, globally reachable"},
		{"2606:4700:4700::1111", true, "a public IPv6"},
		{"::ffff:8.8.8.8", true, "a public IPv4 mapped"},
		{"64:ff9b::808:808", true, "NAT64 of a public IPv4"},
		{"2002:808:808::1", true, "6to4 of a public IPv4"},
		{"0.250.250.254", false, "host.docker.internal of OrbStack"},
		{"198.19.249.3", false, "a machine of OrbStack, benchmarking range"},
		{"192.168.65.254", false, "host.docker.internal of Docker Desktop"},
		{"172.17.0.1", false, "host-gateway of Docker on Linux"},
		{"0.0.0.0", false, "this host"},
		{"0.1.2.3", false, "this network"},
		{"127.0.0.1", false, "loopback"},
		{"10.1.2.3", false, "private"},
		{"100.100.100.100", false, "shared address space, tailnets"},
		{"169.254.169.254", false, "link-local, cloud metadata"},
		{"192.0.0.1", false, "IETF protocol assignments"},
		{"192.0.2.1", false, "TEST-NET-1"},
		{"192.88.99.1", false, "6to4 relay anycast, deprecated"},
		{"198.18.0.1", false, "benchmarking"},
		{"198.51.100.1", false, "TEST-NET-2"},
		{"203.0.113.1", false, "TEST-NET-3"},
		{"224.0.0.1", false, "multicast"},
		{"240.0.0.1", false, "reserved"},
		{"255.255.255.255", false, "limited broadcast"},
		{"::", false, "unspecified"},
		{"::1", false, "loopback"},
		{"::ffff:127.0.0.1", false, "loopback mapped"},
		{"::ffff:0.250.250.254", false, "host.docker.internal of OrbStack mapped"},
		{"::fa:fafe", false, "host.docker.internal of OrbStack IPv4-compatible"},
		{"64:ff9b::fa:fafe", false, "NAT64 of host.docker.internal of OrbStack"},
		{"64:ff9b::7f00:1", false, "NAT64 of loopback"},
		{"64:ff9b:1::808:808", false, "local-use NAT64"},
		{"2002:fa:fafe::1", false, "6to4 of host.docker.internal of OrbStack"},
		{"2002:7f00:1::1", false, "6to4 of loopback"},
		{"100::1", false, "discard-only"},
		{"2001::1", false, "Teredo"},
		{"2001:2::1", false, "benchmarking"},
		{"2001:db8::1", false, "documentation"},
		{"3fff::1", false, "documentation"},
		{"5f00::1", false, "SRv6 SIDs"},
		{"fd12:3456::1", false, "unique local"},
		{"fe80::1", false, "link-local"},
		{"fe80::1%eth0", false, "link-local with a zone"},
		{"fec0::1", false, "site-local, deprecated"},
		{"ff02::1", false, "multicast"},
		{"4000::1", false, "outside global unicast"},
	} {
		if got := Public(netip.MustParseAddr(row.addr)); got != row.public {
			t.Errorf("Public(%s) = %v, want %v: %s", row.addr, got, row.public, row.what)
		}
	}
	if Public(netip.Addr{}) {
		t.Error("Public of no address is false")
	}
}

func Test_listed(t *testing.T) {
	for _, row := range []struct {
		domains  []string
		hostport string
		listed   bool
		what     string
	}{
		{[]string{"example.com"}, "example.com:443", true, "the name itself"},
		{[]string{"example.com"}, "api.example.com:443", true, "a name under it"},
		{[]string{"example.com"}, "notexample.com:443", false, "a name that only ends the same"},
		{[]string{"api.example.com"}, "example.com:443", false, "the parent of a listed name"},
		{[]string{"example.com"}, "EXAMPLE.com.:443", true, "case and the final dot"},
		{[]string{" Example.COM "}, "example.com:80", true, "a listed name with spaces and capitals"},
		{[]string{"example.com"}, "example.com", false, "no port"},
		{[]string{"*"}, "example.com:443", false, "the wildcard names nothing"},
		{nil, "example.com:443", false, "an empty list"},
		{[]string{"example.com:8443"}, "example.com:8443", true, "a name with its port"},
		{[]string{"example.com:8443"}, "example.com:443", false, "a name on another port"},
		{[]string{"10.0.0.0/8"}, "10.1.2.3:80", true, "an address in a listed network"},
		{[]string{"10.0.0.0/8"}, "11.0.0.1:80", false, "an address outside the network"},
		{[]string{"10.0.0.0/8"}, "ten.example:80", false, "a name is not in a network"},
		{[]string{"192.168.1.5"}, "192.168.1.5:22", true, "an address on any port"},
		{[]string{"192.168.1.5"}, "x.192.168.1.5:22", true, "a name under an address, as written"},
		{[]string{"192.168.1.5"}, "192.168.1.50:22", false, "an address that starts the same"},
		{[]string{"127.0.0.1:8080"}, "127.0.0.1:8080", true, "an address with its port"},
		{[]string{"127.0.0.1:8080"}, "127.0.0.1:8081", false, "an address on another port"},
		{[]string{"[::1]:8080"}, "[::1]:8080", true, "an IPv6 address with its port"},
		{[]string{"::1"}, "[::1]:80", true, "a bare IPv6 address"},
		{[]string{"[::1]"}, "[::1]:80", true, "an IPv6 address in brackets"},
		{[]string{"fd00::/8"}, "[fd12::1]:443", true, "an IPv6 address in a listed network"},
		{[]string{"*", "example.org"}, "example.org:443", true, "a name beside the wildcard"},
	} {
		if got := listed(row.domains, row.hostport); got != row.listed {
			t.Errorf("listed(%q, %s) = %v, want %v: %s", row.domains, row.hostport, got, row.listed, row.what)
		}
	}
}
