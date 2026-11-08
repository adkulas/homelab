package engine

import "github.com/adkulas/homelab/internal/config"

type nordVPNSecrets struct {
	OpenVPN struct {
		ServiceUsername string `yaml:"serviceUsername"`
		ServicePassword string `yaml:"servicePassword"`
	} `yaml:"openvpn"`
}

type siteVPNSecretDocument struct {
	Site    string         `yaml:"site"`
	NordVPN nordVPNSecrets `yaml:"nordvpn"`
}

type environmentSecretDocument struct {
	Houndarr struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"houndarr,omitempty"`
	Cleanuparr struct {
		Username string `yaml:"username,omitempty"`
		Password string `yaml:"password,omitempty"`
		APIKey   string `yaml:"apiKey,omitempty"`
	} `yaml:"cleanuparr,omitempty"`
	Scope     *config.SecretScope `yaml:"scope,omitempty"`
	NordVPN   nordVPNSecrets      `yaml:"nordvpn,omitempty"`
	Profilarr struct {
		APIKey string `yaml:"apiKey"`
	} `yaml:"profilarr"`
	Jellyfin struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"jellyfin"`
	QBittorrent struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"qbittorrent"`
}
