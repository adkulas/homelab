# Use .home for local DNS

The home Site uses `.home` for local names, matching its existing Traefik routes and the operator's explicit preference. RFC 8375 instead designates `.home.arpa` for home networks and deprecates unregistered `.home` use because of collisions and leaked queries. We accept that risk; clients using other resolvers may not resolve these names, and the network plan must retain a path to rename the zone if needed.
