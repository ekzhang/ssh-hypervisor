package vm

import (
	"fmt"
	"net"
	"sync"
)

// IPPool manages allocation of IP addresses for VMs
type IPPool struct {
	network   *net.IPNet
	allocated map[string]bool
	available []net.IP
	mu        sync.Mutex
}

// NewIPPool creates a new IP pool from the given network
func NewIPPool(network *net.IPNet) (*IPPool, error) {
	pool := &IPPool{
		network:   network,
		allocated: make(map[string]bool),
		available: make([]net.IP, 0),
	}

	// Generate all usable IPs in the network
	// Skip network address, gateway (.1), and broadcast address
	for ip := network.IP.Mask(network.Mask); network.Contains(ip); inc(ip) {
		if !ip.Equal(network.IP) && !isBroadcast(ip, network) && !isGateway(ip, network) {
			pool.available = append(pool.available, copyIP(ip))
		}
	}

	if len(pool.available) == 0 {
		return nil, fmt.Errorf("no available IP addresses in network %s", network.String())
	}

	return pool, nil
}

// Allocate allocates an IP address from the pool
func (p *IPPool) Allocate() (net.IP, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, ip := range p.available {
		ipStr := ip.String()
		if !p.allocated[ipStr] {
			p.allocated[ipStr] = true
			return ip, nil
		}

		// If we've reached the end, no IPs available
		if i == len(p.available)-1 {
			break
		}
	}

	return nil, fmt.Errorf("no available IP addresses")
}

// Release releases an IP address back to the pool
func (p *IPPool) Release(ip net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ipStr := ip.String()
	delete(p.allocated, ipStr)
}

// IsAllocated checks if an IP address is allocated
func (p *IPPool) IsAllocated(ip net.IP) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.allocated[ip.String()]
}

// Available returns the number of available IP addresses
func (p *IPPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.available) - len(p.allocated)
}

// Gateway returns the gateway IP address (network + 1) for this network
func (p *IPPool) Gateway() net.IP {
	gateway := make(net.IP, len(p.network.IP))
	copy(gateway, p.network.IP.Mask(p.network.Mask))

	// Increment by 1 to get the first host address (gateway)
	inc(gateway)

	return gateway
}

// Netmask returns the subnet mask (e.g. 255.255.255.0) for this network
func (p *IPPool) Netmask() net.IP {
	return net.IP(p.network.Mask)
}

// inc increments an IP address
func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// copyIP creates a copy of an IP address
func copyIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

// isBroadcast checks if an IP is the broadcast address for the network
func isBroadcast(ip net.IP, network *net.IPNet) bool {
	broadcast := make(net.IP, len(network.IP))
	copy(broadcast, network.IP)

	// Set all host bits to 1
	for i := 0; i < len(broadcast); i++ {
		broadcast[i] |= ^network.Mask[i]
	}

	return ip.Equal(broadcast)
}

// isGateway checks if an IP is the gateway address (network + 1) for the network
func isGateway(ip net.IP, network *net.IPNet) bool {
	gateway := make(net.IP, len(network.IP))
	copy(gateway, network.IP.Mask(network.Mask))

	// Increment by 1 to get the first host address (gateway)
	inc(gateway)

	return ip.Equal(gateway)
}
