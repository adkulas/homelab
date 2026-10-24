package siteinventory

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func redactedNetworkPlan(network *NetworkSpec, cluster *NetworkCluster, hosts map[HostName]Host) *RedactedNetworkPlan {
	ids := map[SensitiveValueID]bool{}
	for _, id := range []SensitiveValueID{network.Subnet, network.Gateway, network.DHCP.DynamicStart, network.DHCP.DynamicEnd, network.DHCP.ReservationStart, network.DHCP.ReservationEnd} {
		if id != "" {
			ids[id] = true
		}
	}
	for _, host := range hosts {
		if host.Spec.NetworkAddress != "" {
			ids[host.Spec.NetworkAddress] = true
		}
	}
	for _, reservation := range network.DHCP.Reservations {
		for _, id := range []SensitiveValueID{reservation.Address, reservation.MAC} {
			if id != "" {
				ids[id] = true
			}
		}
	}
	references := make([]RedactedReference, 0, len(ids))
	for id := range ids {
		references = append(references, RedactedReference{ID: id, Value: "<redacted>"})
	}
	sort.Slice(references, func(i, j int) bool { return references[i].ID < references[j].ID })
	return &RedactedNetworkPlan{Primary: cluster.Primary, Secondary: cluster.Secondary, DHCPOwner: network.DHCP.Owner, References: references}
}

func validateDNSCluster(cluster *NetworkCluster, hosts map[HostName]Host, instances []StackInstance, report *Report) {
	if cluster.Primary == cluster.Secondary {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_NETWORK_CLUSTER_INVALID", Explanation: "DNS Cluster primary and secondary are the same Stack Instance.", Remedy: "Reference two distinct Host-assigned DNS Stack Instances."})
	}
	selectedHosts := map[HostName]bool{}
	for _, entry := range []struct {
		name StackInstanceName
		role DNSRole
	}{{cluster.Primary, dnsPrimary}, {cluster.Secondary, dnsSecondary}} {
		instance, exists := resolveDNSMember(instances, entry.name, entry.role)
		if !exists {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_NETWORK_CLUSTER_INVALID", Explanation: fmt.Sprintf("DNS Cluster member %q is invalid.", entry.name), Remedy: "Reference a listed network Stack Instance with the matching DNS role."})
			continue
		}
		if _, exists := hosts[instance.Spec.Host]; !exists || selectedHosts[instance.Spec.Host] {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_NETWORK_CLUSTER_INVALID", Explanation: fmt.Sprintf("DNS Cluster member %q has invalid Host placement.", entry.name), Remedy: "Assign each DNS Stack Instance to a different listed Host."})
		}
		selectedHosts[instance.Spec.Host] = true
	}
}

func resolveDNSMember(instances []StackInstance, name StackInstanceName, role DNSRole) (StackInstance, bool) {
	for _, instance := range instances {
		if instance.Metadata.Name == name && instance.Spec.Stack == "network" && instance.Spec.Network != nil && instance.Spec.Network.Role == role {
			return instance, true
		}
	}
	return StackInstance{}, false
}

// validateNetwork resolves Site-owned topology only for Sites that declare it.
// Diagnostics name semantic entries and never include decrypted values.
func validateNetwork(ctx context.Context, siteDirectory string, network *NetworkSpec, cluster *NetworkCluster, declared *SensitiveValuesSpec, hosts map[HostName]Host, instances []StackInstance, report *Report) {
	add := func(code, entry, remedy string) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: code, Explanation: fmt.Sprintf("network entry %q is invalid.", entry), Remedy: remedy})
	}
	if declared == nil {
		add("SITE_NETWORK_VALUES_MISSING", "sensitiveValues", "Declare a SOPS document with daily and recovery recipients.")
		return
	}
	path, diagnostic := resolveReference(siteDirectory, declared.Document)
	if diagnostic != nil {
		report.Diagnostics = append(report.Diagnostics, *diagnostic)
		return
	}
	encrypted, err := os.ReadFile(path)
	if err != nil {
		add("SITE_NETWORK_VALUES_INVALID", "sensitiveValues", "Read the declared SOPS document and retry.")
		return
	}
	var envelope struct {
		Spec SiteSensitiveValuesSpec `yaml:"spec"`
	}
	if err := yaml.Unmarshal(encrypted, &envelope); err != nil || len(envelope.Spec.Values) == 0 {
		add("SITE_NETWORK_VALUES_INVALID", "sensitiveValues", "Correct the encrypted Site-sensitive values document.")
		return
	}
	unencrypted := false
	for _, entry := range envelope.Spec.Values {
		if !strings.HasPrefix(entry.Value, "ENC[") || !strings.HasSuffix(entry.Value, "]") {
			add("SITE_NETWORK_VALUE_UNENCRYPTED", string(entry.ID), "Encrypt every value field before committing the Site-sensitive values document.")
			unencrypted = true
		}
	}
	if unencrypted {
		return
	}
	contents, err := exec.CommandContext(ctx, "sops", "decrypt", "--output-type", "yaml", path).Output()
	if err != nil {
		add("SITE_NETWORK_DECRYPT_FAILED", "sensitiveValues", "Provide an authorized age identity and retry.")
		return
	}
	values, err := decodeSensitiveValues(contents)
	if err != nil {
		add("SITE_NETWORK_VALUES_INVALID", "sensitiveValues", "Correct the encrypted Site-sensitive values document.")
		return
	}
	value := func(id SensitiveValueID) string {
		if id == "" || values[id] == "" {
			add("SITE_NETWORK_VALUE_MISSING", string(id), "Declare a non-empty referenced value in the encrypted document.")
		}
		return values[id]
	}
	subnetText := value(network.Subnet)
	subnet, err := netip.ParsePrefix(subnetText)
	if err != nil || !subnet.Addr().Is4() || subnet != subnet.Masked() {
		add("SITE_NETWORK_SUBNET_INVALID", string(network.Subnet), "Use a canonical IPv4 LAN prefix.")
		return
	}
	networkAddress := subnet.Addr().As4()
	broadcastBytes := networkAddress
	for bit := subnet.Bits(); bit < 32; bit++ {
		broadcastBytes[bit/8] |= 1 << (7 - bit%8)
	}
	broadcast := netip.AddrFrom4(broadcastBytes)
	parseAddress := func(id SensitiveValueID) netip.Addr {
		text := value(id)
		address, parseErr := netip.ParseAddr(text)
		if parseErr != nil || !address.Is4() || !subnet.Contains(address) || address == subnet.Addr() || address == broadcast {
			add("SITE_NETWORK_ADDRESS_INVALID", string(id), "Use a unique usable IPv4 address inside the declared LAN subnet.")
			return netip.Addr{}
		}
		return address
	}
	addresses := map[netip.Addr]string{}
	claim := func(id SensitiveValueID) netip.Addr {
		address := parseAddress(id)
		if address.IsValid() {
			if first, exists := addresses[address]; exists {
				add("SITE_NETWORK_ADDRESS_DUPLICATE", string(id), "Assign distinct addresses to "+first+" and "+string(id)+".")
			} else {
				addresses[address] = string(id)
			}
		}
		return address
	}
	gateway := claim(network.Gateway)
	for _, role := range []struct {
		name StackInstanceName
		want DNSRole
	}{{cluster.Primary, dnsPrimary}, {cluster.Secondary, dnsSecondary}} {
		instance, exists := resolveDNSMember(instances, role.name, role.want)
		if !exists {
			add("SITE_NETWORK_CLUSTER_INVALID", string(role.name), "Reference a listed network Stack Instance with the matching DNS role.")
			continue
		}
		if hosts[instance.Spec.Host].Spec.NetworkAddress == "" {
			add("SITE_NETWORK_RESOLVER_INVALID", string(role.name), "Give the selected Host a networkAddress reference.")
		}
	}
	if network.DHCP.Owner != cluster.Primary {
		add("SITE_NETWORK_DHCP_OWNER_INVALID", string(network.DHCP.Owner), "Use distinct DNS Stack Instances and assign DHCP to the primary.")
	}
	hostNames := make([]string, 0, len(hosts))
	for name := range hosts {
		hostNames = append(hostNames, string(name))
	}
	sort.Strings(hostNames)
	for _, name := range hostNames {
		if id := hosts[HostName(name)].Spec.NetworkAddress; id != "" {
			claim(id)
		}
	}
	reservedStart := parseAddress(network.DHCP.ReservationStart)
	reservedEnd := parseAddress(network.DHCP.ReservationEnd)
	dynamicStart := parseAddress(network.DHCP.DynamicStart)
	dynamicEnd := parseAddress(network.DHCP.DynamicEnd)
	if reservedStart.IsValid() && reservedEnd.IsValid() && dynamicStart.IsValid() && dynamicEnd.IsValid() {
		if reservedStart.Compare(reservedEnd) > 0 || dynamicStart.Compare(dynamicEnd) > 0 || reservedEnd.Compare(dynamicStart) >= 0 {
			add("SITE_NETWORK_BANDS_INVALID", "dhcp", "Keep an ordered reservation band below a separate dynamic allocation band.")
		}
	}
	if reservedStart.IsValid() && reservedEnd.IsValid() && dynamicStart.IsValid() && dynamicEnd.IsValid() {
		for address, id := range addresses {
			if (address.Compare(reservedStart) >= 0 && address.Compare(reservedEnd) <= 0) || (address.Compare(dynamicStart) >= 0 && address.Compare(dynamicEnd) <= 0) {
				add("SITE_NETWORK_STATIC_OVERLAP", id, "Keep gateway and static Host addresses outside the DHCP scope.")
			}
		}
	}
	reservationNames := map[string]bool{}
	macs := map[string]bool{}
	for _, reservation := range network.DHCP.Reservations {
		if reservation.Name == "" || reservationNames[reservation.Name] {
			add("SITE_NETWORK_RESERVATION_INVALID", reservation.Name, "Give each reservation a unique semantic name.")
		}
		reservationNames[reservation.Name] = true
		address := claim(reservation.Address)
		if address.IsValid() && reservedStart.IsValid() && reservedEnd.IsValid() && (address.Compare(reservedStart) < 0 || address.Compare(reservedEnd) > 0) {
			add("SITE_NETWORK_RESERVATION_INVALID", reservation.Name, "Place reservations inside the excluded reservation band.")
		}
		mac := value(reservation.MAC)
		parsed, parseErr := net.ParseMAC(mac)
		canonical := ""
		if parseErr == nil {
			canonical = parsed.String()
		}
		if parseErr != nil || len(parsed) != 6 || parsed[0]&1 != 0 || macs[canonical] {
			add("SITE_NETWORK_RESERVATION_INVALID", reservation.Name, "Use a unique unicast Ethernet MAC address.")
		}
		if canonical != "" {
			macs[canonical] = true
		}
	}
	if gateway.IsValid() && dynamicStart.IsValid() && dynamicEnd.IsValid() && gateway.Compare(dynamicStart) >= 0 && gateway.Compare(dynamicEnd) <= 0 {
		add("SITE_NETWORK_GATEWAY_INVALID", string(network.Gateway), "Keep the gateway outside dynamic allocation.")
	}
}
