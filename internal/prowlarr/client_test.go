package prowlarr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestReconcileLibraryDiscoveryConvergesPinnedContractFixtures(t *testing.T) {
	indexers := readFixture(t, "indexers.json")
	applications := readFixture(t, "applications.json")
	var mutations []string

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-prowlarr-api-key" {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/system/status":
			_, _ = writer.Write([]byte(`{"appName":"Prowlarr"}`))
		case "GET /api/v1/indexer":
			_, _ = writer.Write(indexers)
		case "POST /api/v1/indexer":
			body, _ := io.ReadAll(request.Body)
			indexers = []byte("[" + string(body) + "]")
			mutations = append(mutations, "internet-archive")
			writer.WriteHeader(http.StatusCreated)
		case "GET /api/v1/applications":
			_, _ = writer.Write(applications)
		case "POST /api/v1/applications":
			body, _ := io.ReadAll(request.Body)
			var created map[string]any
			_ = json.Unmarshal(body, &created)
			if created["name"] == "Radarr" {
				syncCategories, present := fieldValues(created["fields"])["syncCategories"].([]any)
				if !present || len(syncCategories) == 0 {
					http.Error(writer, "'Sync Categories' must not be empty.", http.StatusBadRequest)
					return
				}
			}
			if string(applications) == "[]\n" {
				applications = []byte("[" + string(body) + "]")
			} else {
				applications = append(applications[:len(applications)-1], append([]byte(","+string(body)), ']')...)
			}
			mutations = append(mutations, strings.ToLower(created["name"].(string))+"-application")
			writer.WriteHeader(http.StatusCreated)
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	})

	client := New("http://prowlarr:9696", "fixture-prowlarr-api-key", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	changed, err := client.ReconcileLibraryDiscovery(context.Background(), "fixture-radarr-api-key", "fixture-sonarr-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first reconciliation reported no changes")
	}
	if want := []string{"internet-archive", "radarr-application", "sonarr-application"}; !reflect.DeepEqual(mutations, want) {
		t.Fatalf("mutations = %v, want %v", mutations, want)
	}

	var createdIndexers []map[string]any
	if err := json.Unmarshal(indexers, &createdIndexers); err != nil {
		t.Fatal(err)
	}
	if createdIndexers[0]["definitionName"] != "internetarchive" {
		t.Fatalf("Public Torrent Source definition = %v", createdIndexers[0])
	}
	indexerFields := fieldValues(createdIndexers[0]["fields"])
	if createdIndexers[0]["appProfileId"] != float64(1) || indexerFields["sort"] != float64(2) || indexerFields["type"] != float64(1) {
		t.Fatalf("Public Torrent Source pinned contract = %v", createdIndexers[0])
	}
	var createdApplications []map[string]any
	if err := json.Unmarshal(applications, &createdApplications); err != nil {
		t.Fatal(err)
	}
	fields := fieldValues(createdApplications[0]["fields"])
	if fields["baseUrl"] != "http://radarr:7878" || fields["prowlarrUrl"] != "http://prowlarr:9696" || fields["apiKey"] != "fixture-radarr-api-key" {
		t.Fatalf("Radarr application contract = %v", fields)
	}
	wantMovieCategories := []any{float64(2000), float64(2010), float64(2020), float64(2030), float64(2040), float64(2045), float64(2050), float64(2060), float64(2070), float64(2080), float64(2090)}
	if !reflect.DeepEqual(fields["syncCategories"], wantMovieCategories) {
		t.Fatalf("Radarr category sync contract = %v", fields)
	}
	fields = fieldValues(createdApplications[1]["fields"])
	if fields["baseUrl"] != "http://sonarr:8989" || fields["prowlarrUrl"] != "http://prowlarr:9696" || fields["apiKey"] != "fixture-sonarr-api-key" {
		t.Fatalf("Sonarr application contract = %v", fields)
	}
	wantSeriesCategories := []any{float64(5000), float64(5010), float64(5020), float64(5030), float64(5040), float64(5045), float64(5050), float64(5090)}
	if !reflect.DeepEqual(fields["syncCategories"], wantSeriesCategories) || !reflect.DeepEqual(fields["animeSyncCategories"], []any{float64(5070)}) || fields["syncAnimeStandardFormatSearch"] != true || fields["syncRejectBlocklistedTorrentHashesWhileGrabbing"] != false {
		t.Fatalf("Sonarr category sync contract = %v", fields)
	}

	mutations = nil
	changed, err = client.ReconcileLibraryDiscovery(context.Background(), "fixture-radarr-api-key", "fixture-sonarr-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(mutations) != 0 {
		t.Fatalf("repeated reconciliation changed state: changed=%v mutations=%v", changed, mutations)
	}
}

func fieldValues(raw any) map[string]any {
	values := map[string]any{}
	for _, item := range raw.([]any) {
		field := item.(map[string]any)
		values[field["name"].(string)] = field["value"]
	}
	return values
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := &responseWriter{header: make(http.Header), status: http.StatusOK}
	transport.handler.ServeHTTP(recorder, request)
	return &http.Response{StatusCode: recorder.status, Header: recorder.header, Body: io.NopCloser(&recorder.body), Request: request}, nil
}

type responseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (writer *responseWriter) Header() http.Header            { return writer.header }
func (writer *responseWriter) Write(body []byte) (int, error) { return writer.body.Write(body) }
func (writer *responseWriter) WriteHeader(status int)         { writer.status = status }

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
