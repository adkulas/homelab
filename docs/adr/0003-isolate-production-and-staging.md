# Isolate Production and Staging Media Stacks

Production and Staging will be separate Compose projects with distinct Gluetun, qBittorrent, application containers, config
volumes, secrets, ports, and `/data` subtrees. The duplication costs more resources, but it permits real acquisition, upgrade
testing, and Restore Drills without allowing Staging state or scheduled jobs to act on Production.
