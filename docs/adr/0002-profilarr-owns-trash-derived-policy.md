# Use Profilarr for TRaSH-derived media policy

Profilarr alone will own Sonarr and Radarr quality profiles, custom formats, quality definitions, naming, and media-management
policy using a pinned TRaSH-derived PCD source; Recyclarr is excluded to prevent competing writers. This accepts that
Profilarr is not currently an officially listed TRaSH synchronization tool and that its documented API cannot create Arr
connections, so each environment requires a one-time guided UI bootstrap followed by environment-specific full-state backups.

Request-time routing is a separate responsibility. Seerr may assign the Profilarr-synchronized profile and per-item request
values when it creates a movie or series, but it does not define or modify the synchronized profile or Radarr/Sonarr
service-wide media-management policy.
