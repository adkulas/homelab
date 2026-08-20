package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStartsQBittorrentOnlyAfterHealthyGluetunWithRuntimeSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	preferences := map[string]any{}
	categories := map[string]map[string]string{}
	var rootFolders []map[string]any
	var downloadClients []map[string]any
	moviePolicyObservations := map[string]int{
		"qualityprofile":    0,
		"customformat":      0,
		"qualitydefinition": 0,
		"naming":            0,
		"mediamanagement":   0,
	}
	movieQualityProfiles := []map[string]any{
		{
			"name":              "HD Bluray + WEB",
			"upgradeAllowed":    true,
			"cutoff":            7,
			"minFormatScore":    0,
			"cutoffFormatScore": 10000,
			"items": []any{
				map[string]any{"quality": map[string]any{"id": 7, "name": "Bluray-1080p"}, "allowed": true},
				map[string]any{"name": "WEB 1080p", "allowed": true, "items": []any{}},
				map[string]any{"quality": map[string]any{"id": 6, "name": "Bluray-720p"}, "allowed": true},
			},
			"formatItems": []any{
				map[string]any{"format": 101, "score": 1800},
				map[string]any{"format": 102, "score": 1700},
				map[string]any{"format": 103, "score": -10000},
			},
		},
	}
	movieCustomFormats := []map[string]any{
		{"id": 101, "name": "HD Bluray Tier 01"},
		{"id": 102, "name": "WEB Tier 01"},
		{"id": 103, "name": "BR-DISK"},
	}
	movieQualityDefinitions := []map[string]any{
		{"quality": map[string]any{"name": "Bluray-1080p"}, "minSize": 51, "maxSize": 2000, "preferredSize": 1999},
		{"quality": map[string]any{"name": "WEBDL-1080p"}, "minSize": 13, "maxSize": 2000, "preferredSize": 1999},
	}
	movieNaming := map[string]any{
		"renameMovies":             true,
		"standardMovieFormat":      "{Movie CleanTitle} {(Release Year)} [tmdbid-{TmdbId}] - {{Edition Tags}} {[MediaInfo 3D]}{[Custom Formats]}{[Quality Full]}{[Mediainfo AudioCodec}{ Mediainfo AudioChannels]}{[MediaInfo VideoDynamicRangeType]}{[Mediainfo VideoCodec]}{-Release Group}",
		"movieFolderFormat":        "{Movie CleanTitle} ({Release Year}) [tmdbid-{TmdbId}]",
		"replaceIllegalCharacters": false,
		"colonReplacementFormat":   "smart",
	}
	movieMediaManagement := map[string]any{
		"downloadPropersAndRepacks": "doNotPrefer",
		"enableMediaInfo":           true,
	}
	var indexers []map[string]any
	var applications []map[string]any
	profilarrInstances := []map[string]any{
		{"id": 1, "name": "Radarr", "type": "radarr", "url": "http://radarr:7878", "enabled": 1},
		{"id": 2, "name": "Sonarr", "type": "sonarr", "url": "http://sonarr:8989", "enabled": 1},
	}
	profilarrObservations := 0
	var seriesRootFolders []map[string]any
	var seriesDownloadClients []map[string]any
	seriesPolicyObservations := map[string]int{
		"qualityprofile":    0,
		"customformat":      0,
		"qualitydefinition": 0,
		"naming":            0,
		"mediamanagement":   0,
	}
	seriesQualityProfiles := []map[string]any{
		{
			"name":              "HD Bluray + WEB",
			"upgradeAllowed":    true,
			"cutoff":            7,
			"minFormatScore":    0,
			"cutoffFormatScore": 10000,
			"items": []any{
				map[string]any{"quality": map[string]any{"id": 7, "name": "Bluray-1080p"}, "allowed": true},
				map[string]any{"name": "WEB 1080p", "allowed": true, "items": []any{}},
				map[string]any{"quality": map[string]any{"id": 6, "name": "Bluray-720p"}, "allowed": true},
			},
			"formatItems": []any{
				map[string]any{"format": 101, "score": 1800},
				map[string]any{"format": 102, "score": 1700},
				map[string]any{"format": 103, "score": -10000},
			},
		},
	}
	seriesCustomFormats := []map[string]any{
		{"id": 101, "name": "HD Bluray Tier 01"},
		{"id": 102, "name": "WEB Tier 01"},
		{"id": 103, "name": "BR-DISK"},
	}
	seriesQualityDefinitions := []map[string]any{
		{"quality": map[string]any{"name": "Bluray-1080p"}, "minSize": 51, "maxSize": 2000, "preferredSize": 1999},
		{"quality": map[string]any{"name": "WEBDL-1080p"}, "minSize": 13, "maxSize": 2000, "preferredSize": 1999},
	}
	seriesNaming := map[string]any{
		"renameMovies":             true,
		"standardMovieFormat":      "{Movie CleanTitle} {(Release Year)} [tmdbid-{TmdbId}] - {{Edition Tags}} {[MediaInfo 3D]}{[Custom Formats]}{[Quality Full]}{[Mediainfo AudioCodec}{ Mediainfo AudioChannels]}{[MediaInfo VideoDynamicRangeType]}{[Mediainfo VideoCodec]}{-Release Group}",
		"movieFolderFormat":        "{Movie CleanTitle} ({Release Year}) [tmdbid-{TmdbId}]",
		"replaceIllegalCharacters": false,
		"colonReplacementFormat":   "smart",
	}
	seriesMediaManagement := map[string]any{
		"downloadPropersAndRepacks": "doNotPrefer",
		"enableMediaInfo":           true,
	}
	sonarrAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-sonarr-api-key" {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v3/system/status":
			_ = json.NewEncoder(writer).Encode(map[string]string{"appName": "Sonarr"})
		case "GET /api/v3/qualityprofile":
			seriesPolicyObservations["qualityprofile"]++
			_ = json.NewEncoder(writer).Encode(seriesQualityProfiles)
		case "GET /api/v3/customformat":
			seriesPolicyObservations["customformat"]++
			_ = json.NewEncoder(writer).Encode(seriesCustomFormats)
		case "GET /api/v3/qualitydefinition":
			seriesPolicyObservations["qualitydefinition"]++
			_ = json.NewEncoder(writer).Encode(seriesQualityDefinitions)
		case "GET /api/v3/config/naming":
			seriesPolicyObservations["naming"]++
			_ = json.NewEncoder(writer).Encode(seriesNaming)
		case "GET /api/v3/config/mediamanagement":
			seriesPolicyObservations["mediamanagement"]++
			_ = json.NewEncoder(writer).Encode(seriesMediaManagement)
		case "GET /api/v3/rootfolder":
			_ = json.NewEncoder(writer).Encode(seriesRootFolders)
		case "POST /api/v3/rootfolder":
			var root map[string]any
			_ = json.NewDecoder(request.Body).Decode(&root)
			seriesRootFolders = append(seriesRootFolders, root)
			writer.WriteHeader(http.StatusCreated)
		case "GET /api/v3/downloadclient":
			_ = json.NewEncoder(writer).Encode(seriesDownloadClients)
		case "POST /api/v3/downloadclient":
			var client map[string]any
			_ = json.NewDecoder(request.Body).Decode(&client)
			seriesDownloadClients = append(seriesDownloadClients, client)
			writer.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer sonarrAPI.Close()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/arr/instances" {
			profilarrObservations++
			if profilarrObservations == 1 {
				http.Error(writer, "starting", http.StatusServiceUnavailable)
				return
			}
			if request.Header.Get("X-Api-Key") != "fixture-profilarr-api-key-32-characters" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(writer).Encode(profilarrInstances)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/v1/") {
			if request.Header.Get("X-Api-Key") != "fixture-prowlarr-api-key" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch request.Method + " " + request.URL.Path {
			case "GET /api/v1/system/status":
				_ = json.NewEncoder(writer).Encode(map[string]string{"appName": "Prowlarr"})
			case "GET /api/v1/indexer":
				_ = json.NewEncoder(writer).Encode(indexers)
			case "POST /api/v1/indexer":
				var indexer map[string]any
				_ = json.NewDecoder(request.Body).Decode(&indexer)
				indexers = append(indexers, indexer)
				writer.WriteHeader(http.StatusCreated)
			case "GET /api/v1/applications":
				_ = json.NewEncoder(writer).Encode(applications)
			case "POST /api/v1/applications":
				var application map[string]any
				_ = json.NewDecoder(request.Body).Decode(&application)
				applications = append(applications, application)
				writer.WriteHeader(http.StatusCreated)
			default:
				http.NotFound(writer, request)
			}
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/v3/") {
			if request.Header.Get("X-Api-Key") != "fixture-radarr-api-key" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch request.Method + " " + request.URL.Path {
			case "GET /api/v3/system/status":
				_ = json.NewEncoder(writer).Encode(map[string]string{"appName": "Radarr"})
			case "GET /api/v3/rootfolder":
				_ = json.NewEncoder(writer).Encode(rootFolders)
			case "POST /api/v3/rootfolder":
				var root map[string]any
				_ = json.NewDecoder(request.Body).Decode(&root)
				rootFolders = append(rootFolders, root)
				writer.WriteHeader(http.StatusCreated)
			case "GET /api/v3/downloadclient":
				_ = json.NewEncoder(writer).Encode(downloadClients)
			case "POST /api/v3/downloadclient":
				var client map[string]any
				_ = json.NewDecoder(request.Body).Decode(&client)
				downloadClients = append(downloadClients, client)
				writer.WriteHeader(http.StatusCreated)
			case "GET /api/v3/qualityprofile":
				moviePolicyObservations["qualityprofile"]++
				_ = json.NewEncoder(writer).Encode(movieQualityProfiles)
			case "GET /api/v3/customformat":
				moviePolicyObservations["customformat"]++
				_ = json.NewEncoder(writer).Encode(movieCustomFormats)
			case "GET /api/v3/qualitydefinition":
				moviePolicyObservations["qualitydefinition"]++
				_ = json.NewEncoder(writer).Encode(movieQualityDefinitions)
			case "GET /api/v3/config/naming":
				moviePolicyObservations["naming"]++
				_ = json.NewEncoder(writer).Encode(movieNaming)
			case "GET /api/v3/config/mediamanagement":
				moviePolicyObservations["mediamanagement"]++
				_ = json.NewEncoder(writer).Encode(movieMediaManagement)
			default:
				http.NotFound(writer, request)
			}
			return
		}
		if request.URL.Path == "/api/v2/auth/login" {
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("username") != "admin" || values.Get("password") != "fixture-temporary-password" {
				http.Error(writer, "Fails.", http.StatusForbidden)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "acceptance-session", Path: "/"})
			_, _ = writer.Write([]byte("Ok."))
			return
		}
		cookie, err := request.Cookie("SID")
		if err != nil || cookie.Value != "acceptance-session" {
			http.Error(writer, "Forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v2/app/preferences":
			_ = json.NewEncoder(writer).Encode(preferences)
		case "/api/v2/app/setPreferences":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			_ = json.Unmarshal([]byte(values.Get("json")), &preferences)
		case "/api/v2/torrents/categories":
			_ = json.NewEncoder(writer).Encode(categories)
		case "/api/v2/torrents/createCategory":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			name := values.Get("category")
			categories[name] = map[string]string{"name": name, "savePath": values.Get("savePath")}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	apiURL, err := url.Parse(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	declared := strings.Replace(string(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"))), "qbittorrent: 18080", "qbittorrent: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "radarr: 17878", "radarr: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "prowlarr: 19696", "prowlarr: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "profilarr: 16868", "profilarr: "+apiURL.Port(), 1)
	sonarrURL, err := url.Parse(sonarrAPI.URL)
	if err != nil {
		t.Fatal(err)
	}
	declared = strings.Replace(declared, "sonarr: 18989", "sonarr: "+sonarrURL.Port(), 1)
	writeFile(t, configPath, []byte(declared), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
cat <<'EOF'
nordvpn:
  openvpn:
    serviceUsername: apply-service-user
    servicePassword: apply-service-password
profilarr:
  apiKey: fixture-profilarr-api-key-32-characters
EOF
`), 0o700)
	dockerLog := filepath.Join(temporary, "docker-arguments")
	composeCapture := filepath.Join(temporary, "compose.yaml")
	healthCount := filepath.Join(temporary, "health-count")
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
	printf '%s\n' "$*" >> "$APPLY_DOCKER_LOG"
	cat > "$APPLY_COMPOSE_CAPTURE"
	case "$*" in
	  "compose -f - up -d gluetun") exit 0 ;;
	  "compose -f - up -d qbittorrent") exit 0 ;;
	  "compose -f - logs --no-color qbittorrent") printf 'A temporary password is provided for this session: fixture-temporary-password\n'; exit 0 ;;
	  "compose -f - up -d radarr") exit 0 ;;
	  "compose -f - exec -T radarr cat /config/config.xml") printf '<Config><ApiKey>fixture-radarr-api-key</ApiKey></Config>\n'; exit 0 ;;
	  "compose -f - up -d sonarr") exit 0 ;;
	  "compose -f - exec -T sonarr cat /config/config.xml") printf '<Config><ApiKey>fixture-sonarr-api-key</ApiKey></Config>\n'; exit 0 ;;
	  "compose -f - up -d prowlarr") exit 0 ;;
	  "compose -f - exec -T prowlarr cat /config/config.xml") printf '<Config><ApiKey>fixture-prowlarr-api-key</ApiKey></Config>\n'; exit 0 ;;
	  "compose -f - up -d profilarr") exit 0 ;;
	  "compose -f - ps --format json gluetun")
	    count=0
	    [ -f "$APPLY_HEALTH_COUNT" ] && count=$(cat "$APPLY_HEALTH_COUNT")
	    count=$((count + 1))
	    printf '%s' "$count" > "$APPLY_HEALTH_COUNT"
	    [ "$count" -eq 1 ] && printf '{"Health":"unhealthy","State":"running"}\n' || printf '{"Health":"healthy","State":"running"}\n'
	    exit 0 ;;
	  *) exit 99 ;;
	esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
		"APPLY_DOCKER_LOG="+dockerLog,
		"APPLY_COMPOSE_CAPTURE="+composeCapture,
		"APPLY_HEALTH_COUNT="+healthCount,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack apply failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Applied the pinned Movie Library policy through Profilarr in the staging Environment.") {
		t.Errorf("apply output = %q, want completed Movie Library policy", output)
	}

	wantDocker := "compose -f - up -d gluetun\ncompose -f - ps --format json gluetun\ncompose -f - ps --format json gluetun\ncompose -f - up -d qbittorrent\ncompose -f - logs --no-color qbittorrent\ncompose -f - up -d radarr\ncompose -f - exec -T radarr cat /config/config.xml\ncompose -f - up -d sonarr\ncompose -f - exec -T sonarr cat /config/config.xml\ncompose -f - up -d prowlarr\ncompose -f - exec -T prowlarr cat /config/config.xml\ncompose -f - up -d profilarr"
	if got := strings.TrimSpace(string(readFile(t, dockerLog))); got != wantDocker {
		t.Errorf("Docker invocation = %q", got)
	}
	rendered := string(readFile(t, composeCapture))
	for _, secret := range []string{"apply-service-user", "apply-service-password", "fixture-profilarr-api-key-32-characters"} {
		if strings.Contains(rendered, secret) || strings.Contains(string(output), secret) {
			t.Errorf("apply exposed secret %q\noutput: %s\nCompose: %s", secret, output, rendered)
		}
	}
	if strings.Contains(string(output), "fixture-temporary-password") {
		t.Errorf("apply exposed qBittorrent temporary password: %s", output)
	}

	runtimeDirectory := filepath.Join(temporary, "runtime", "media-stack", "media-staging")
	if info, statErr := os.Stat(runtimeDirectory); statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime secret directory mode = %v, %v; want 0700", info, statErr)
	}
	for name, want := range map[string]string{
		"openvpn_user":     "apply-service-user",
		"openvpn_password": "apply-service-password",
		"profilarr.env":    "PROFILARR_API_KEY=fixture-profilarr-api-key-32-characters",
	} {
		path := filepath.Join(runtimeDirectory, name)
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("runtime secret %s mode = %v, %v; want 0600", name, info, statErr)
		}
		if got := strings.TrimSpace(string(readFile(t, path))); got != want {
			t.Errorf("runtime secret %s = %q, want selected credential", name, got)
		}
	}
	if preferences["save_path"] != "/data/torrents" || categories["movies"]["savePath"] != "movies" || categories["series"]["savePath"] != "series" {
		t.Errorf("apply did not reconcile qBittorrent acquisition policy: preferences=%v categories=%v", preferences, categories)
	}
	if len(rootFolders) != 1 || rootFolders[0]["path"] != "/data/media/movies" {
		t.Errorf("apply did not reconcile the Movie Library root folder: %v", rootFolders)
	}
	if len(downloadClients) != 1 {
		t.Fatalf("apply did not reconcile Radarr's qBittorrent client: %v", downloadClients)
	}
	fields := map[string]any{}
	for _, item := range downloadClients[0]["fields"].([]any) {
		field := item.(map[string]any)
		fields[field["name"].(string)] = field["value"]
	}
	if fields["host"] != "qbittorrent" || fields["port"] != float64(8080) || fields["movieCategory"] != "movies" {
		t.Errorf("Radarr qBittorrent contract = %v", fields)
	}
	if len(seriesRootFolders) != 1 || seriesRootFolders[0]["path"] != "/data/media/series" {
		t.Errorf("apply did not reconcile the Series Library root folder: %v", seriesRootFolders)
	}
	if len(seriesDownloadClients) != 1 {
		t.Fatalf("apply did not reconcile Sonarr's qBittorrent client: %v", seriesDownloadClients)
	}
	seriesFields := map[string]any{}
	for _, item := range seriesDownloadClients[0]["fields"].([]any) {
		field := item.(map[string]any)
		seriesFields[field["name"].(string)] = field["value"]
	}
	if seriesFields["host"] != "qbittorrent" || seriesFields["port"] != float64(8080) || seriesFields["tvCategory"] != "series" {
		t.Errorf("Sonarr qBittorrent contract = %v", seriesFields)
	}
	if len(indexers) != 1 || indexers[0]["definitionName"] != "internetarchive" {
		t.Fatalf("apply did not reconcile the approved Public Torrent Source: %v", indexers)
	}
	if len(applications) != 2 {
		t.Fatalf("apply did not reconcile Prowlarr application links: %v", applications)
	}
	applicationsByName := map[string]map[string]any{}
	for _, application := range applications {
		applicationFields := map[string]any{}
		for _, item := range application["fields"].([]any) {
			field := item.(map[string]any)
			applicationFields[field["name"].(string)] = field["value"]
		}
		applicationsByName[application["name"].(string)] = applicationFields
	}
	if fields := applicationsByName["Radarr"]; fields["baseUrl"] != "http://radarr:7878" || fields["prowlarrUrl"] != "http://prowlarr:9696" || fields["apiKey"] != "fixture-radarr-api-key" {
		t.Errorf("Prowlarr Radarr application contract = %v", fields)
	}
	if fields := applicationsByName["Sonarr"]; fields["baseUrl"] != "http://sonarr:8989" || fields["prowlarrUrl"] != "http://prowlarr:9696" || fields["apiKey"] != "fixture-sonarr-api-key" {
		t.Errorf("Prowlarr Sonarr application contract = %v", fields)
	}
	if profilarrObservations != 2 {
		t.Errorf("apply observed Profilarr connections %d times, want startup retry plus successful verification", profilarrObservations)
	}
	for observation, count := range moviePolicyObservations {
		if count != 1 {
			t.Errorf("apply observed Radarr %s %d times, want once", observation, count)
		}
	}
	for observation, count := range seriesPolicyObservations {
		if count != 1 {
			t.Errorf("apply observed Sonarr %s %d times, want once", observation, count)
		}
	}
	secondCommand := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	secondCommand.Dir = command.Dir
	secondCommand.Env = command.Env
	secondOutput, err := secondCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("repeated media-stack apply failed: %v\n%s", err, secondOutput)
	}
	if len(rootFolders) != 1 || len(downloadClients) != 1 || len(seriesRootFolders) != 1 || len(seriesDownloadClients) != 1 || len(indexers) != 1 || len(applications) != 2 {
		t.Errorf("repeated apply did not converge: movieRoots=%v movieDownloadClients=%v seriesRoots=%v seriesDownloadClients=%v indexers=%v applications=%v", rootFolders, downloadClients, seriesRootFolders, seriesDownloadClients, indexers, applications)
	}

	originalMovieQualityProfiles := movieQualityProfiles
	originalSeriesQualityProfiles := seriesQualityProfiles
	movieQualityProfiles = nil
	policyCommand := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	policyCommand.Dir = command.Dir
	policyCommand.Env = command.Env
	policyOutput, err := policyCommand.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted Movie Library policy drift: %s", policyOutput)
	}
	for _, want := range []string{
		"manual action required",
		"http://127.0.0.1:" + apiURL.Port(),
		"https://github.com/Dictionarry-Hub/trash-pcd",
		"9e424382191de7d507efc9806ac3c807793d1c60",
		"HD Bluray + WEB",
		"Jellyfin TMDB",
		"Movie quality definitions",
		"Default",
		"run sync",
		"rerun media-stack apply",
		"Movie Library policy drift",
	} {
		if !strings.Contains(string(policyOutput), want) {
			t.Errorf("Movie Library policy guidance = %q, want %q", policyOutput, want)
		}
	}
	seriesQualityProfiles = nil
	seriesPolicyCommand := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	seriesPolicyCommand.Dir = command.Dir
	seriesPolicyCommand.Env = command.Env
	seriesPolicyOutput, err := seriesPolicyCommand.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted Series Library policy drift: %s", seriesPolicyOutput)
	}
	for _, want := range []string{
		"manual action required",
		"http://127.0.0.1:" + apiURL.Port(),
		"https://github.com/Dictionarry-Hub/trash-pcd",
		"9e424382191de7d507efc9806ac3c807793d1c60",
		"HD Bluray + WEB",
		"Jellyfin TMDB",
		"Series quality definitions",
		"Default",
		"run sync",
		"rerun media-stack apply",
		"Series Library policy drift",
	} {
		if !strings.Contains(string(seriesPolicyOutput), want) {
			t.Errorf("Series Library policy guidance = %q, want %q", seriesPolicyOutput, want)
		}
	}
	movieQualityProfiles = originalMovieQualityProfiles
	seriesQualityProfiles = originalSeriesQualityProfiles
	profilarrInstances = profilarrInstances[:1]
	thirdCommand := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	thirdCommand.Dir = command.Dir
	thirdCommand.Env = command.Env
	thirdOutput, err := thirdCommand.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted an incomplete Profilarr bootstrap: %s", thirdOutput)
	}
	for _, want := range []string{"manual action required", "http://127.0.0.1:" + apiURL.Port(), "http://radarr:7878", "http://sonarr:8989", "rerun media-stack apply"} {
		if !strings.Contains(string(thirdOutput), want) {
			t.Errorf("incomplete Profilarr guidance = %q, want %q", thirdOutput, want)
		}
	}
}

func TestApplyRedactsCredentialsFromUnhealthyGluetunFailure(t *testing.T) {
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "media-stack")
	build := exec.Command("go", "build", "-o", binary, "./cmd/media-stack")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build media-stack: %v\n%s", err, output)
	}
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
printf 'nordvpn:\n  openvpn:\n    serviceUsername: failure-service-user\n    servicePassword: failure-service-password\nprofilarr:\n  apiKey: fixture-profilarr-api-key-32-characters\n'
`), 0o700)
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
cat >/dev/null
printf 'authentication failed for failure-service-user with failure-service-password\n' >&2
exit 1
`), 0o700)

	command := exec.Command(binary, "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted unhealthy Gluetun: %s", output)
	}
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
		t.Errorf("unhealthy Gluetun exit = %v, want operational exit 1", err)
	}
	for _, secret := range []string{"failure-service-user", "failure-service-password"} {
		if strings.Contains(string(output), secret) {
			t.Errorf("apply failure exposed secret %q: %s", secret, output)
		}
	}
	if !strings.Contains(string(output), "start healthy Gluetun") || !strings.Contains(string(output), "[REDACTED]") {
		t.Errorf("apply failure is not actionable and redacted: %s", output)
	}
}
