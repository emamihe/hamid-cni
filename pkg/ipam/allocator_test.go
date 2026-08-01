package ipam

import (
	"net"
	"testing"
)

func TestNextIPSkipsUsed(t *testing.T) {
	_, cidr, err := net.ParseCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{
		"10.0.0.0": true, // network
		"10.0.0.1": true,
	}
	ip, err := nextIP(cidr, used)
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "10.0.0.2" {
		t.Fatalf("got %s want 10.0.0.2", ip)
	}
}

func TestIPAllocName(t *testing.T) {
	name := ipAllocName("vpc-blue", net.ParseIP("10.0.0.5"))
	if name != "vpc-blue-10-0-0-5" {
		t.Fatalf("got %q", name)
	}
}

func TestBroadcast(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/24")
	b := broadcast(cidr)
	if b.String() != "10.0.0.255" {
		t.Fatalf("got %s", b)
	}
}
