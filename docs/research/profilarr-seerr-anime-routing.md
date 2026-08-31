# Profilarr, Seerr, and anime request routing

Research performed 2026-08-31 against this repository's pinned Profilarr 2.1.0, Seerr 3.4.1, Sonarr 4.0.19.2979,
and Prowlarr 2.5.2.5491 images. The Profilarr image corresponds to upstream commit
[`395544b5dbd24c78f13a09226a87fe532ceb50d2`](https://github.com/Dictionarry-Hub/profilarr/commit/395544b5dbd24c78f13a09226a87fe532ceb50d2),
and the Seerr image labels identify upstream commit
[`69f73a6f1486fdb51b8ddae9a94a8dfb629f461c`](https://github.com/seerr-team/seerr/commit/69f73a6f1486fdb51b8ddae9a94a8dfb629f461c).
[Repository pins](../../stacks/media/versions.yaml)

## Answer

Profilarr does not choose a quality profile for an individual anime series. It compiles a selected PCD quality profile and
creates or updates the **profile definition** in Sonarr by name through Sonarr's `/api/v3/qualityprofile` API. The sync also
pushes the custom formats referenced by that profile before it builds the profile payload. Profilarr's series update methods
in this version are used for tags and folder renames; the quality-profile sync path never updates a series'
`qualityProfileId`. [Profilarr quality-profile syncer](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/sync/qualityProfiles/syncer.ts#L287-L330)
[Profilarr Arr quality-profile API methods](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/utils/arr/base.ts#L268-L299)
[Profilarr Sonarr series operations](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/utils/arr/clients/sonarr.ts#L249-L275)

Seerr performs the assignment when it adds a new series to Sonarr. For a series it identifies as anime, Seerr selects the
configured anime Sonarr profile ID and anime root folder, then sends those as `qualityProfileId` and `rootFolderPath` in
Sonarr's add-series payload. Therefore the practical setup order for one Sonarr instance is:

1. Sync an anime quality-profile definition from Profilarr into that Sonarr instance.
2. In Seerr's Sonarr service settings, test the connection and select the resulting Sonarr profile under **Anime Quality
   Profile**; also select the anime root folder and set **Anime Series Type** to `Anime`.
3. Leave the ordinary profile/root settings as the defaults for non-anime series.

Seerr stores the numeric Sonarr profile ID, not the Profilarr profile name. If a profile is deleted and recreated rather than
updated in place, Seerr's saved ID can become stale and must be reselected. Also, when a series already exists in Sonarr,
Seerr 3.4.1 only updates monitoring, requested seasons, and tags; it does not replace that existing series' profile, root
folder, or series type. Existing series with the wrong assignment must be changed in Sonarr (or by another explicit
reconciliation path). [Seerr Sonarr settings submission](https://github.com/seerr-team/seerr/blob/v3.4.1/src/components/Settings/SonarrModal/index.tsx#L275-L321)
[Seerr add-or-update behavior](https://github.com/seerr-team/seerr/blob/v3.4.1/server/api/servarr/sonarr.ts#L193-L289)

This repository cannot yet perform that anime-specific setup as declared. Its current Profilarr Series Policy syncs only
`WEB-1080p` and explicitly excludes anime-specific profiles. The presence of an `animeEpisodeFormat` controls naming for
series whose Sonarr type is anime; it does not create or select an anime quality profile. The current Seerr client reconciles
authentication only and does not configure a Sonarr server, profile, root folder, or anime defaults. Consequently the
current stack needs a new approved anime profile in the Profilarr policy plus a Seerr service-settings reconciliation or a
documented manual step before anime routing is complete. [Current Profilarr Series Policy](../../stacks/media/fixtures/profilarr-series-policy.yaml)
[Current Seerr reconciliation](../../internal/seerr/client.go)

## How Seerr selects an anime destination

For an approved series request, pinned Seerr 3.4.1 applies the following precedence:

1. It selects the default Sonarr server whose `is4k` value matches the request. A request-level `serverId` overrides that
   default. [Sonarr server selection](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L489-L527)
2. It fetches the title from TMDB and treats it as anime when TMDB returns keyword ID `210024`. The configured
   `animeSeriesType` is then used, defaulting to `anime` when unset. [Anime keyword](https://github.com/seerr-team/seerr/blob/v3.4.1/server/api/themoviedb/constants.ts)
   [Anime detection and series type](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L568-L585)
3. When the resulting series type is `anime`, it selects `activeAnimeDirectory`, `activeAnimeProfileId`, anime language
   profile, and anime tags; missing anime settings fall back to the ordinary directory/profile/language settings (tags fall
   back to an empty list). [Anime defaults](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L587-L607)
4. Values persisted on the request itself override the selected root folder, quality profile, language profile, and tags.
   Advanced Request initializes its controls from the selected server's anime defaults, while configured override rules can
   also place profile/root/tag overrides on a request. [Request override precedence](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L609-L645)
   [Advanced Request defaults](https://github.com/seerr-team/seerr/blob/v3.4.1/src/components/RequestModal/AdvancedRequester/index.tsx#L180-L230)
   [Override-rule application](https://github.com/seerr-team/seerr/blob/v3.4.1/server/entity/MediaRequest.ts#L235-L359)
5. Seerr calls Sonarr with the selected profile, root folder, series type, tags, requested seasons, monitoring settings, and
   `searchForMissingEpisodes` according to Seerr's Enable Search setting. [Seerr Sonarr request options](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L698-L721)
   [Sonarr add-series payload](https://github.com/seerr-team/seerr/blob/v3.4.1/server/api/servarr/sonarr.ts#L254-L289)

There is a pinned-version edge case: Seerr 3.4.1 gates the anime profile/root selection on the resulting
`seriesType === 'anime'`, not directly on the TMDB anime classification. Setting **Anime Series Type** to `Standard` causes
automatic processing to use the ordinary defaults unless request-level overrides were saved. For the intended split, set it
to `Anime`.

## Actual request and acquisition flow

The runtime flow is not `Seerr -> Prowlarr -> downloader`. It is:

```text
Seerr --add series/profile/root/search flag--> Sonarr
                                                |
                    search --------------------> Prowlarr Torznab endpoint --> Public Torrent Source
                    release result <------------ Prowlarr proxy result <-----+
                    grab link -----------------> Prowlarr download proxy ----> Public Torrent Source
                    torrent/magnet ------------> qBittorrent
                    completed-download poll <--> qBittorrent
                    import --------------------> Series Library
```

The responsibilities are:

- **Seerr talks only to Sonarr for this acquisition path.** It sends the series metadata and policy choices to Sonarr and
  records the returned Sonarr series identity. Seerr's source contains no Prowlarr or qBittorrent call in this path.
  [Seerr request dispatch](https://github.com/seerr-team/seerr/blob/v3.4.1/server/subscriber/MediaRequestSubscriber.ts#L698-L737)
- **Prowlarr configures Sonarr ahead of requests.** A full-sync Prowlarr application link creates or updates a separate
  Newznab/Torznab indexer entry in Sonarr for each eligible Prowlarr indexer. The generated entry points Sonarr back to
  `<prowlarr-url>/<indexer-id>/api`, carries Prowlarr's API key, and includes the supported standard and anime categories.
  [Prowlarr Sonarr indexer construction](https://github.com/Prowlarr/Prowlarr/blob/v2.5.2.5491/src/NzbDrone.Core/Applications/Sonarr/Sonarr.cs#L241-L282)
  [Prowlarr writes Sonarr's indexer API](https://github.com/Prowlarr/Prowlarr/blob/v2.5.2.5491/src/NzbDrone.Core/Applications/Sonarr/SonarrV3Proxy.cs#L45-L104)
- **Sonarr owns searching and release acceptance.** When the add-series option requests a search, Sonarr queues a missing
  episode search after the initial series scan. Its search service asks the configured indexers for candidates, applies
  Sonarr's decision engine—including the quality profile and custom-format scores assigned to the series—and processes the
  accepted decision as a grab. [Sonarr post-add search](https://github.com/Sonarr/Sonarr/blob/v4.0.19.2979/src/NzbDrone.Core/Tv/SeriesScannedHandler.cs#L43-L74)
  [Sonarr episode search and decision processing](https://github.com/Sonarr/Sonarr/blob/v4.0.19.2979/src/NzbDrone.Core/IndexerSearch/EpisodeSearchService.cs#L40-L94)
- **Prowlarr is Sonarr's indexer proxy.** Its Torznab endpoint translates Sonarr's query to the selected source and rewrites
  result download/magnet URLs to protected Prowlarr proxy links. When Sonarr grabs one, Prowlarr resolves that link and
  downloads or redirects to the actual source. [Prowlarr Torznab search and link rewriting](https://github.com/Prowlarr/Prowlarr/blob/v2.5.2.5491/src/Prowlarr.Api.V1/Indexers/NewznabController.cs#L161-L207)
  [Prowlarr protected proxy-link construction](https://github.com/Prowlarr/Prowlarr/blob/v2.5.2.5491/src/NzbDrone.Core/Download/DownloadMappingService.cs#L17-L48)
  [Prowlarr release-download endpoint](https://github.com/Prowlarr/Prowlarr/blob/v2.5.2.5491/src/Prowlarr.Api.V1/Indexers/NewznabController.cs#L209-L279)
- **Sonarr talks directly to qBittorrent.** Sonarr selects a compatible configured download client, obtains the release from
  the indexer/proxy, and calls the download client's `Download` implementation. Its qBittorrent client then adds either the
  magnet URL or torrent file through qBittorrent's API. Sonarr tracks the download and imports it after qBittorrent reports
  completion. [Sonarr download-client selection and dispatch](https://github.com/Sonarr/Sonarr/blob/v4.0.19.2979/src/NzbDrone.Core/Download/DownloadService.cs#L53-L117)
  [Sonarr qBittorrent submission](https://github.com/Sonarr/Sonarr/blob/v4.0.19.2979/src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs#L71-L157)
  [Sonarr completed-download processing](https://github.com/Sonarr/Sonarr/blob/v4.0.19.2979/src/NzbDrone.Core/Download/CompletedDownloadService.cs#L57-L119)

In this repository specifically, `media-stack apply` full-syncs Sonarr's standard categories plus anime category `5070`
through Prowlarr, and separately configures qBittorrent as a download client directly in Sonarr with category `series`.
Those are independent connections. Prowlarr enables discovery; it neither receives the Seerr request nor decides the
quality profile, download client, or import destination. [Local Prowlarr application link](../../internal/prowlarr/client.go)
[Local Sonarr download-client configuration](../../internal/sonarr/client.go)

## Implications for this stack

1. A second Sonarr instance is not required merely to apply an anime profile. One Sonarr can hold ordinary and anime
   quality profiles, and Seerr has distinct ordinary/anime defaults for the same Sonarr service.
2. Add an explicitly approved anime quality profile to the Profilarr Series Policy and sync it before configuring Seerr.
   The existing anime filename format and Prowlarr category `5070` do not substitute for that profile.
3. Reconcile or guide the Seerr Sonarr-service settings: internal URL/API key, default server, ordinary and anime profile IDs,
   ordinary and anime root folders, Anime Series Type, season folders, tags, monitoring, and Enable Search. The current
   authentication-only Seerr reconciliation does not establish the acquisition path.
4. Verification should request a known TMDB-title carrying anime keyword `210024`, then prove Sonarr received the intended
   `seriesType`, `qualityProfileId`, and root path before testing discovery or download. This separates routing failures from
   Prowlarr/indexer and qBittorrent failures.
5. Profile selection and indexer categories solve different problems: the quality profile determines which releases Sonarr
   accepts and prefers; category `5070` determines which anime results an indexer exposes to Sonarr.
