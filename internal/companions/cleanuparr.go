package companions

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/qbittorrent"
)

const maskedCredential = "••••••••"

// Cleanuparr manages the supported v2.10.12 configuration API.
type Cleanuparr struct{ api api }

func NewCleanuparr(base, key string, client *http.Client) *Cleanuparr {
	return &Cleanuparr{api: newAPI(base, key, client)}
}

type change struct {
	method, path string
	body         any
}

// Reconcile inspects all settings first, pauses queue cleaning during changes,
// and enables it only after connection and policy validation succeeds.
func (client *Cleanuparr) Reconcile(ctx context.Context, policy config.SeriesAutomation, sonarrKey string, qbit qbittorrent.DeclaredConfiguration, observeOnly bool) error {
	if client.api.key == "" {
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Complete Cleanuparr account setup on the selected Environment loopback endpoint and store cleanuparr.apiKey in its SOPS document.")
	}
	var queue, seeker, arr, downloads document
	for _, read := range []struct {
		path   string
		target *document
	}{{"/api/configuration/queue_cleaner", &queue}, {"/api/configuration/seeker", &seeker}, {"/api/configuration/sonarr", &arr}, {"/api/configuration/download_client", &downloads}} {
		if err := client.api.request(ctx, "GET", read.path, nil, read.target); err != nil {
			return err
		}
	}
	for _, name := range []string{"radarr", "lidarr", "readarr", "whisparr", "sportarr", "lazylibrarian"} {
		var other document
		if err := client.api.request(ctx, "GET", "/api/configuration/"+name, nil, &other); err != nil {
			return err
		}
		if len(array(other["instances"])) > 0 {
			return fail("VERIFY_COMPANION_TARGET_INVALID", "Remove other Arr connections from this Environment's dedicated Cleanuparr before applying series-only recovery.")
		}
	}
	connections := array(arr["instances"])
	clients := array(downloads["clients"])
	if len(connections) > 1 || len(clients) > 1 {
		return fail("VERIFY_COMPANION_TARGET_INVALID", "Keep exactly one Environment-local Sonarr and qBittorrent connection in Cleanuparr.")
	}
	var changes []change
	desiredArr := document{"enabled": policy.Enabled, "name": "Sonarr", "url": "http://sonarr:8989", "version": 4, "externalUrl": nil}
	desiredQbit := document{"enabled": policy.Enabled, "name": "qBittorrent", "typeName": "qBittorrent", "host": "http://qbittorrent:" + strconv.Itoa(qbit.Port), "username": qbit.Credentials.Username, "urlBase": nil, "externalUrl": nil, "downloadDirectorySource": nil, "downloadDirectoryTarget": nil}
	arrID := ""
	if len(connections) == 1 {
		current := connections[0]
		arrID, _ = current["id"].(string)
		if strings.TrimRight(stringField(current, "url"), "/") != "http://sonarr:8989" {
			return fail("VERIFY_COMPANION_TARGET_INVALID", "Correct Cleanuparr's Sonarr URL to http://sonarr:8989 before enabling recovery.")
		}
		test := document{"instanceId": arrID, "url": "http://sonarr:8989", "version": 4, "apiKey": maskedCredential}
		connectionErr := client.api.request(ctx, "POST", "/api/configuration/sonarr/instances/test", test, nil)
		current["url"] = strings.TrimRight(stringField(current, "url"), "/")
		if !includes(current, desiredArr) || connectionErr != nil {
			body := merge(current, desiredArr)
			body["apiKey"] = sonarrKey
			changes = append(changes, change{"PUT", "/api/configuration/sonarr/instances/" + arrID, body})
		}
	} else {
		body := merge(nil, desiredArr)
		body["apiKey"] = sonarrKey
		changes = append(changes, change{"POST", "/api/configuration/sonarr/instances", body})
	}
	if len(clients) == 1 {
		current := clients[0]
		id := stringField(current, "id")
		if strings.TrimRight(stringField(current, "host"), "/") != desiredQbit["host"] {
			return fail("VERIFY_COMPANION_TARGET_INVALID", "Correct Cleanuparr's qBittorrent target to this Environment's internal alias and declared port.")
		}
		test := merge(current, document{"clientId": id, "password": maskedCredential})
		connectionErr := client.api.request(ctx, "POST", "/api/configuration/download_client/test", test, nil)
		current["host"] = strings.TrimRight(stringField(current, "host"), "/")
		if !includes(current, desiredQbit) || connectionErr != nil {
			body := merge(current, desiredQbit)
			body["password"] = qbit.Credentials.Password
			changes = append(changes, change{"PUT", "/api/configuration/download_client/" + id, body})
		}
	} else {
		body := merge(nil, desiredQbit)
		body["password"] = qbit.Credentials.Password
		changes = append(changes, change{"POST", "/api/configuration/download_client", body})
	}
	for _, kind := range []string{"stall", "slow"} {
		var rules []document
		if err := client.api.request(ctx, "GET", "/api/queue-rules/"+kind, nil, &rules); err != nil {
			return err
		}
		desired := document{"name": "Media Stack " + kind, "enabled": true, "maxStrikes": policy.Recovery.Strikes, "privacyType": "Public", "minCompletionPercentage": 0, "maxCompletionPercentage": 100, "deletePrivateTorrentsFromClient": false, "changeCategory": false, "resetStrikesOnProgress": true}
		if kind == "stall" {
			desired["minimumProgress"] = "1MB"
		} else {
			desired["minSpeed"] = fmt.Sprintf("%dKB", policy.Recovery.MinimumSpeedKiB)
			desired["maxTimeHours"] = policy.Recovery.MaximumRemainingHours
			desired["ignoreAboveSize"] = nil
			desired["ignoreWhileAltSpeedActive"] = true
		}
		found := false
		for _, rule := range rules {
			if rule["name"] == desired["name"] {
				found = true
				if !includes(rule, desired) {
					changes = append(changes, change{"PUT", "/api/queue-rules/" + kind + "/" + stringField(rule, "id"), merge(rule, desired)})
				}
			} else {
				changes = append(changes, change{"DELETE", "/api/queue-rules/" + kind + "/" + stringField(rule, "id"), nil})
			}
		}
		if !found {
			changes = append(changes, change{"POST", "/api/queue-rules/" + kind, desired})
		}
	}
	desiredQueue := document{"enabled": policy.Enabled, "cronExpression": fmt.Sprintf("0 0/%d * * * ?", policy.Recovery.IntervalMinutes), "useAdvancedScheduling": false, "downloadingMetadataMaxStrikes": policy.Recovery.Strikes, "processNoContentId": false, "ignoredDownloads": []string{}, "errored": document{"maxStrikes": 0}, "failedImport": document{"maxStrikes": policy.Recovery.Strikes, "ignorePrivate": true, "deletePrivate": false, "skipIfNotFoundInClient": true, "patterns": []string{"No video files were found", "Not a valid video file"}, "patternMode": "Include", "changeCategory": false, "forceImport": false, "forceImportMaxTries": 3}}
	if !includes(arr, document{"failedImportMaxStrikes": -1}) {
		changes = append(changes, change{"PUT", "/api/configuration/sonarr", document{"failedImportMaxStrikes": -1}})
	}
	for _, path := range []string{"download_cleaner", "malware_blocker", "blacklist_sync"} {
		var current document
		if err := client.api.request(ctx, "GET", "/api/configuration/"+path, nil, &current); err != nil {
			return err
		}
		if current["enabled"] != false {
			changes = append(changes, change{"PUT", "/api/configuration/" + path, merge(current, document{"enabled": false})})
		}
	}
	var general document
	if err := client.api.request(ctx, "GET", "/api/configuration/general", nil, &general); err != nil {
		return err
	}
	desiredGeneral := document{"dryRun": false, "statusCheckEnabled": true, "ignoredDownloads": []string{}}
	if !includes(general, desiredGeneral) {
		changes = append(changes, change{"PUT", "/api/configuration/general", merge(general, desiredGeneral)})
	}
	desiredSeeker := merge(seeker, document{"searchEnabled": policy.Enabled, "searchInterval": 2, "proactiveSearchEnabled": false})
	instances := array(seeker["instances"])
	for _, instance := range instances {
		instance["enabled"] = false
	}
	desiredSeeker["instances"] = instances
	seekerDiffers := !includes(seeker, document{"searchEnabled": policy.Enabled, "searchInterval": 2, "proactiveSearchEnabled": false})
	// Compare the original response separately; do not let slice mutation hide drift.
	var originalSeeker document
	if err := client.api.request(ctx, "GET", "/api/configuration/seeker", nil, &originalSeeker); err != nil {
		return err
	}
	for _, instance := range array(originalSeeker["instances"]) {
		if instance["enabled"] != false {
			seekerDiffers = true
		}
	}
	if len(changes) == 0 && !seekerDiffers && includes(queue, desiredQueue) {
		return nil
	}
	if observeOnly {
		if seekerDiffers {
			return fail("VERIFY_DUPLICATE_PROACTIVE_OWNER", "Run apply to disable Cleanuparr proactive Seeker and restore replacement searching.")
		}
		detail := "queue policy"
		if len(changes) > 0 {
			detail = changes[0].method + " " + changes[0].path
		}
		return fail("VERIFY_COMPANION_POLICY_DRIFT", "Run apply to restore declared queue health and connection settings; first difference: "+detail+".")
	}
	if queue["enabled"] == true {
		if err := client.api.request(ctx, "PUT", "/api/configuration/queue_cleaner", merge(queue, document{"enabled": false}), nil); err != nil {
			return err
		}
	}
	// Stop duplicate proactive searches before editing any other policy.
	desiredSeeker["proactiveSearchEnabled"] = false
	if err := client.api.request(ctx, "PUT", "/api/configuration/seeker", desiredSeeker, nil); err != nil {
		return err
	}
	for _, operation := range changes {
		if err := client.api.request(ctx, operation.method, operation.path, operation.body, nil); err != nil {
			return err
		}
	}
	// Creating the Sonarr connection creates a per-instance Seeker record. Re-read
	// it before disabling the instance, otherwise its upstream default is enabled.
	var refreshed document
	if err := client.api.request(ctx, "GET", "/api/configuration/seeker", nil, &refreshed); err != nil {
		return err
	}
	refreshedInstances := array(refreshed["instances"])
	for _, instance := range refreshedInstances {
		instance["enabled"] = false
	}
	refreshed["instances"] = refreshedInstances
	refreshed["searchEnabled"], refreshed["searchInterval"], refreshed["proactiveSearchEnabled"] = policy.Enabled, 2, false
	if err := client.api.request(ctx, "PUT", "/api/configuration/seeker", refreshed, nil); err != nil {
		return err
	}
	if err := client.api.request(ctx, "PUT", "/api/configuration/queue_cleaner", merge(queue, desiredQueue), nil); err != nil {
		return err
	}
	return client.Reconcile(ctx, policy, sonarrKey, qbit, true)
}

func array(value any) []document {
	values, _ := value.([]any)
	result := make([]document, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			copy := document{}
			for key, value := range item {
				copy[key] = value
			}
			result = append(result, copy)
		}
	}
	return result
}
func stringField(value document, key string) string { text, _ := value[key].(string); return text }
