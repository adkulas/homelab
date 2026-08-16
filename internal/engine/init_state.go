package engine

import (
	"time"

	"github.com/adkulas/homelab/internal/config"
)

func initializationComplete(declared config.MediaStack) bool {
	defaults := declared.Spec.Defaults
	vpn := declared.Spec.Acquisition.VPN
	if defaults.RuntimeUID <= 0 || defaults.RuntimeGID <= 0 || defaults.Timezone == "" {
		return false
	}
	if _, err := time.LoadLocation(defaults.Timezone); err != nil {
		return false
	}
	if vpn.Provider != "nordvpn" || vpn.Protocol != "openvpn" {
		return false
	}
	if vpn.OpenVPNProtocol != "udp" && vpn.OpenVPNProtocol != "tcp" {
		return false
	}
	if len(vpn.Server.Countries) != 1 {
		return false
	}
	interval, err := time.ParseDuration(vpn.CatalogueUpdateInterval)
	return err == nil && interval >= 360*time.Hour
}
