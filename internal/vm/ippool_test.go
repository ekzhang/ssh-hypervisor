package vm

import (
	"net"
	"testing"
)

func TestNewIPPool(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.100.0/24")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	pool, err := NewIPPool(network)
	if err != nil {
		t.Fatalf("Failed to create IP pool: %v", err)
	}

	// Should have 254 available IPs (256 - network - broadcast)
	if pool.Available() != 254 {
		t.Errorf("Expected 254 available IPs, got %d", pool.Available())
	}
}

func TestIPPoolAllocation(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.100.0/28")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	pool, err := NewIPPool(network)
	if err != nil {
		t.Fatalf("Failed to create IP pool: %v", err)
	}

	// Should have 14 available IPs (/28 = 16 - 2)
	expectedAvailable := 14
	if pool.Available() != expectedAvailable {
		t.Errorf("Expected %d available IPs, got %d", expectedAvailable, pool.Available())
	}

	// Allocate an IP
	ip1, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	if !network.Contains(ip1) {
		t.Errorf("Allocated IP %s is not in network %s", ip1, network)
	}

	if pool.Available() != expectedAvailable-1 {
		t.Errorf("Expected %d available IPs after allocation, got %d", expectedAvailable-1, pool.Available())
	}

	// Check if IP is marked as allocated
	if !pool.IsAllocated(ip1) {
		t.Errorf("IP %s should be marked as allocated", ip1)
	}

	// Allocate another IP
	ip2, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate second IP: %v", err)
	}

	if ip1.Equal(ip2) {
		t.Errorf("Allocated the same IP twice: %s", ip1)
	}

	// Release the first IP
	pool.Release(ip1)

	if pool.IsAllocated(ip1) {
		t.Errorf("IP %s should not be marked as allocated after release", ip1)
	}

	if pool.Available() != expectedAvailable-1 {
		t.Errorf("Expected %d available IPs after release, got %d", expectedAvailable-1, pool.Available())
	}
}

func TestIPPoolExhaustion(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.100.0/30")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	pool, err := NewIPPool(network)
	if err != nil {
		t.Fatalf("Failed to create IP pool: %v", err)
	}

	// /30 has only 2 usable IPs
	expectedAvailable := 2
	if pool.Available() != expectedAvailable {
		t.Errorf("Expected %d available IPs, got %d", expectedAvailable, pool.Available())
	}

	// Allocate both IPs
	ip1, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate first IP: %v", err)
	}

	_, err = pool.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate second IP: %v", err)
	}

	// Try to allocate a third IP - should fail
	_, err = pool.Allocate()
	if err == nil {
		t.Errorf("Expected error when allocating from exhausted pool")
	}

	// Release one and try again
	pool.Release(ip1)
	ip3, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate after release: %v", err)
	}

	if !ip1.Equal(ip3) {
		t.Errorf("Expected to get back the same IP after release: %s != %s", ip1, ip3)
	}
}

func TestIPPoolInvalidNetwork(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.100.0/31")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	// /31 has no usable IPs (only network and broadcast)
	_, err = NewIPPool(network)
	if err == nil {
		t.Errorf("Expected error when creating pool with /31 network")
	}
}