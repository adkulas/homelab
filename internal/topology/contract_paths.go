package topology

func servicePortSource(service string) string {
	if service == "gluetun" {
		service = "qbittorrent"
	}
	return "media-stack.yaml#spec.environments.<environment>.ports." + service
}
