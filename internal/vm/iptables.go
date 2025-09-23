package vm

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

// cleanupIptablesRules removes any existing iptables rules with the "ssh-hypervisor" comment
func cleanupIptablesRules() error {
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("failed to initialize iptables: %w", err)
	}

	// Clean up FORWARD rules
	if err := cleanupRulesWithComment(ipt, "filter", "FORWARD"); err != nil {
		return fmt.Errorf("failed to clean up FORWARD rules: %w", err)
	}

	// Clean up NAT POSTROUTING rules
	if err := cleanupRulesWithComment(ipt, "nat", "POSTROUTING"); err != nil {
		return fmt.Errorf("failed to clean up POSTROUTING rules: %w", err)
	}

	return nil
}

// cleanupRulesWithComment removes rules from a specific table/chain that contain the "ssh-hypervisor" comment
func cleanupRulesWithComment(ipt *iptables.IPTables, table, chain string) error {
	rules, err := ipt.List(table, chain)
	if err != nil {
		return err
	}

	// Find rules with our comment (iterate backwards to avoid index issues when deleting)
	for i := len(rules) - 1; i >= 0; i-- {
		rule := rules[i]
		if strings.Contains(rule, "ssh-hypervisor") {
			// Parse the rule to remove the line number and chain name prefix
			parts := strings.Fields(rule)
			if len(parts) > 2 && (parts[0] == "-A" || strings.HasPrefix(parts[0], "-A")) {
				// Skip the chain name and build the rule spec
				ruleSpec := parts[2:]
				if err := ipt.Delete(table, chain, ruleSpec...); err != nil {
					// Log but don't fail if rule doesn't exist
					continue
				}
			}
		}
	}

	return nil
}

// setupIptablesRules configures the necessary iptables rules for VM networking
func (m *Manager) setupIptablesRules() error {
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("failed to initialize iptables: %w", err)
	}

	// Get the VM network CIDR
	vmNet, err := m.config.GetVMIPRange()
	if err != nil {
		return fmt.Errorf("failed to get VM IP range: %w", err)
	}

	// Add FORWARD rules
	// iptables -A FORWARD -i sshvm-br0 ! -o sshvm-br0 -j ACCEPT -m comment --comment "ssh-hypervisor"
	if err := ipt.Append("filter", "FORWARD", "-i", m.bridgeName, "!", "-o", m.bridgeName, "-j", "ACCEPT", "-m", "comment", "--comment", "ssh-hypervisor"); err != nil {
		return fmt.Errorf("failed to add FORWARD rule (outbound): %w", err)
	}

	// iptables -A FORWARD ! -i sshvm-br0 -o sshvm-br0 -j ACCEPT -m comment --comment "ssh-hypervisor"
	if err := ipt.Append("filter", "FORWARD", "!", "-i", m.bridgeName, "-o", m.bridgeName, "-j", "ACCEPT", "-m", "comment", "--comment", "ssh-hypervisor"); err != nil {
		return fmt.Errorf("failed to add FORWARD rule (inbound): %w", err)
	}

	// Add NAT POSTROUTING rule
	// iptables -t nat -A POSTROUTING -s <VM_CIDR> ! -o sshvm-br0 -j MASQUERADE -m comment --comment "ssh-hypervisor"
	if err := ipt.Append("nat", "POSTROUTING", "-s", vmNet.String(), "!", "-o", m.bridgeName, "-j", "MASQUERADE", "-m", "comment", "--comment", "ssh-hypervisor"); err != nil {
		return fmt.Errorf("failed to add POSTROUTING rule: %w", err)
	}

	m.logger.Infof("Configured iptables rules for bridge %s and network %s", m.bridgeName, vmNet.String())
	return nil
}
