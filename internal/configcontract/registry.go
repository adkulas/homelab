package configcontract

import (
	"github.com/adkulas/homelab/internal/contractmodel"
	"github.com/adkulas/homelab/internal/engine"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/prowlarr"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/seerr"
	"github.com/adkulas/homelab/internal/sonarr"
)

type serviceRegistration struct {
	title    string
	settings func() []contractmodel.Setting
}

var serviceRegistry = map[string]serviceRegistration{
	"gluetun":     {title: "Gluetun"},
	"jellyfin":    {title: "Jellyfin", settings: jellyfin.ConfigurationContract},
	"profilarr":   {title: "Profilarr", settings: engine.ProfilarrConfigurationContract},
	"prowlarr":    {title: "Prowlarr", settings: prowlarr.ConfigurationContract},
	"qbittorrent": {title: "qBittorrent", settings: qbittorrent.ConfigurationContract},
	"radarr":      {title: "Radarr", settings: radarrSettings},
	"seerr":       {title: "Seerr", settings: seerr.ConfigurationContract},
	"sonarr":      {title: "Sonarr", settings: sonarrSettings},
}

func radarrSettings() []contractmodel.Setting {
	settings := radarr.ConfigurationContract()
	return append(settings, engine.ArrPolicyRevisionContract())
}

func sonarrSettings() []contractmodel.Setting {
	settings := sonarr.ConfigurationContract()
	return append(settings, engine.ArrPolicyRevisionContract())
}
