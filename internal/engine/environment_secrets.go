package engine

type environmentSecretDocument struct {
	NordVPN struct {
		OpenVPN struct {
			ServiceUsername string `yaml:"serviceUsername"`
			ServicePassword string `yaml:"servicePassword"`
		} `yaml:"openvpn"`
	} `yaml:"nordvpn"`
	Profilarr struct {
		APIKey string `yaml:"apiKey"`
	} `yaml:"profilarr"`
	Jellyfin struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"jellyfin"`
}
