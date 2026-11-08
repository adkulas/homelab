package topology

func servicePortSource(service string) string {
	if service == "gluetun" {
		service = "qbittorrent"
	}
	return "StackInstance#spec.media.environments.<environment>.ports." + service
}
