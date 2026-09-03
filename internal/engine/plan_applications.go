package engine

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/prowlarr"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/seerr"
	"github.com/adkulas/homelab/internal/sonarr"
)

var errPlannedMutation = errors.New("application configuration differs")

type readOnlyTransport struct {
	base http.RoundTripper
}

func (transport readOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet || request.Method == http.MethodHead || observationPost(request.URL.Path) {
		return transport.base.RoundTrip(request)
	}
	return nil, errPlannedMutation
}

func observationPost(path string) bool {
	return path == "/api/v2/auth/login" ||
		path == "/Users/AuthenticateByName" ||
		path == "/api/v1/auth/jellyfin" ||
		path == "/api/v1/auth/local" ||
		strings.HasSuffix(path, "/test")
}

func observationHTTPClient() *http.Client {
	return &http.Client{Transport: readOnlyTransport{base: http.DefaultTransport}, Timeout: 10 * time.Second}
}

// ObserveConfiguration uses the same reconciliation contracts as apply while
// blocking every state-changing application request at the HTTP boundary.
func ObserveConfiguration(ctx context.Context, request PlanRequest, plan Plan) []PlanAction {
	actions := ObserveTopology(ctx, plan)
	for _, action := range actions {
		switch action.Kind {
		case planActionCreate, planActionRestart, planActionUpdate, planActionDeferred:
			return actions
		}
	}
	return append(actions, observeApplications(ctx, request, plan)...)
}

func observeApplications(ctx context.Context, request PlanRequest, plan Plan) []PlanAction {
	declared, err := config.Load(request.configPath)
	if err != nil {
		return deferredApplication("applications", err)
	}
	environment := declared.Spec.Environments[request.environment]
	secrets, err := decryptSelectedEnvironmentSecrets(ctx, request.configPath, environment)
	if err != nil {
		return deferredApplication("applications", err)
	}
	versions, err := config.LoadVersions(request.versionsPath)
	if err != nil {
		return deferredApplication("applications", err)
	}
	moviePolicy, err := radarr.LoadMoviePolicy(filepath.Join(filepath.Dir(request.versionsPath), "fixtures", "profilarr-movie-policy.yaml"), versions.Policy.ProfilarrPCDRevision)
	if err != nil {
		return deferredApplication("radarr", err)
	}
	seriesPolicy, err := sonarr.LoadSeriesPolicy(filepath.Join(filepath.Dir(request.versionsPath), "fixtures", "profilarr-series-policy.yaml"), versions.Policy.ProfilarrPCDRevision)
	if err != nil {
		return deferredApplication("sonarr", err)
	}

	actions := make([]PlanAction, 0)
	qbitAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.QBittorrent)
	qbitClient := qbittorrent.New("http://"+qbitAddress, observationHTTPClient())
	if err := qbitClient.Login(ctx, secrets.QBittorrent.Username, secrets.QBittorrent.Password); err != nil {
		actions = append(actions, deferredApplication("qbittorrent", err)...)
	} else {
		_, err = qbitClient.ReconcileAcquisitionPolicy(ctx)
		actions = append(actions, applicationPlanAction("qbittorrent", err)...)
	}
	qbitConfiguration := qbittorrent.DeclaredConfiguration{Credentials: secrets.QBittorrent, Port: environment.Ports.QBittorrent}

	radarrKey, radarrKeyErr := observeServiceAPIKey(ctx, plan, "radarr")
	sonarrKey, sonarrKeyErr := observeServiceAPIKey(ctx, plan, "sonarr")
	prowlarrKey, prowlarrKeyErr := observeServiceAPIKey(ctx, plan, "prowlarr")

	if radarrKeyErr != nil {
		actions = append(actions, deferredApplication("radarr", radarrKeyErr)...)
	} else {
		client := radarr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Radarr), radarrKey, observationHTTPClient())
		_, err = client.ReconcileMovieLibrary(ctx, qbitConfiguration)
		actions = append(actions, applicationPlanAction("radarr", err)...)
		if err == nil {
			actions = append(actions, policyPlanAction("radarr", client.VerifyMoviePolicy(ctx, moviePolicy))...)
		}
	}
	if sonarrKeyErr != nil {
		actions = append(actions, deferredApplication("sonarr", sonarrKeyErr)...)
	} else {
		client := sonarr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Sonarr), sonarrKey, observationHTTPClient())
		_, err = client.ReconcileSeriesLibrary(ctx, qbitConfiguration)
		actions = append(actions, applicationPlanAction("sonarr", err)...)
		if err == nil {
			actions = append(actions, policyPlanAction("sonarr", client.VerifySeriesPolicy(ctx, seriesPolicy))...)
		}
	}
	if prowlarrKeyErr != nil || radarrKeyErr != nil || sonarrKeyErr != nil {
		actions = append(actions, deferredApplication("prowlarr", errors.Join(prowlarrKeyErr, radarrKeyErr, sonarrKeyErr))...)
	} else {
		client := prowlarr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Prowlarr), prowlarrKey, observationHTTPClient())
		_, err = client.ReconcileLibraryDiscovery(ctx, radarrKey, sonarrKey)
		actions = append(actions, applicationPlanAction("prowlarr", err)...)
	}

	profilarrAddress := "http://" + environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Profilarr)
	complete, _, profilarrErr := observeProfilarrBootstrap(ctx, profilarrAddress, secrets.ProfilarrKey)
	if profilarrErr != nil {
		actions = append(actions, deferredApplication("profilarr", profilarrErr)...)
	} else if !complete {
		actions = append(actions, PlanAction{Kind: planActionGuide, Subject: "profilarr", Explanation: "declared Radarr and Sonarr connections require guided reconciliation"})
	}

	jellyfinClient := jellyfin.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin), observationHTTPClient())
	actions = append(actions, applicationPlanAction("jellyfin", jellyfinClient.ReconcileLibraries(ctx, secrets.Jellyfin))...)

	seerrClient, err := seerr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Seerr), observationHTTPClient())
	if err != nil {
		actions = append(actions, deferredApplication("seerr", err)...)
	} else {
		credentials := seerr.Credentials{Username: secrets.Jellyfin.Username, Password: secrets.Jellyfin.Password}
		err = seerrClient.ReconcileAuthentication(ctx, credentials)
		if err == nil && radarrKeyErr == nil {
			err = seerrClient.ReconcileRadarr(ctx, radarrKey, moviePolicy.Profile.Name)
		}
		if err == nil && sonarrKeyErr == nil {
			err = seerrClient.ReconcileSonarr(ctx, sonarrKey, seriesPolicy.Profile.Name)
		}
		actions = append(actions, applicationPlanAction("seerr", err)...)
	}
	return actions
}

func observeServiceAPIKey(ctx context.Context, plan Plan, service string) (string, error) {
	output, err := runDockerCompose(ctx, plan, "exec", "-T", service, "cat", "/config/config.xml")
	if err != nil {
		return "", err
	}
	var document struct {
		APIKey string `xml:"ApiKey"`
	}
	if err := xml.Unmarshal(output, &document); err != nil || document.APIKey == "" {
		return "", fmt.Errorf("%s did not publish an API key", service)
	}
	return document.APIKey, nil
}

func applicationPlanAction(service string, err error) []PlanAction {
	if err == nil {
		return nil
	}
	if errors.Is(err, errPlannedMutation) {
		return []PlanAction{{Kind: planActionUpdate, Subject: service, Explanation: "application settings differ from Declared Configuration"}}
	}
	return deferredApplication(service, err)
}

func policyPlanAction(service string, err error) []PlanAction {
	if err == nil {
		return nil
	}
	return []PlanAction{{Kind: planActionGuide, Subject: service, Explanation: "externally synchronized policy differs from the Service Configuration Contract"}}
}

func deferredApplication(service string, err error) []PlanAction {
	return []PlanAction{{Kind: planActionDeferred, Subject: service, Explanation: "application observation is unavailable: " + err.Error()}}
}
