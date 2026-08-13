# Use a cross-platform Compose Media Stack

The repository will organize independently deployable domain stacks under `stacks/`, beginning with `stacks/media/`, rather
than remain a NixOS host configuration or become one repository-wide Compose project. Docker Compose owns container topology,
while a Go CLI owns portable setup, API reconciliation, verification, backup, and restore on Ubuntu and Docker Desktop through
WSL; this preserves one workflow across target hosts while allowing future stacks to evolve independently.
