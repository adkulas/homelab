package sonarr

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

func TestReconcileSeriesLibraryConvergesContractFixtures(t *testing.T) {
	rootFolders := readFixture(t, "root-folders.json")
	downloadClients := readFixture(t, "download-clients.json")
	var mutations []string

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-api-key" {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v3/system/status":
			_, _ = writer.Write([]byte(`{"appName":"Sonarr"}`))
		case "GET /api/v3/rootfolder":
			_, _ = writer.Write(rootFolders)
		case "POST /api/v3/rootfolder":
			body, _ := io.ReadAll(request.Body)
			rootFolders = []byte("[" + string(body) + "]")
			mutations = append(mutations, "root-folder")
			writer.WriteHeader(http.StatusCreated)
		case "GET /api/v3/downloadclient":
			_, _ = writer.Write(downloadClients)
		case "POST /api/v3/downloadclient":
			body, _ := io.ReadAll(request.Body)
			downloadClients = []byte("[" + string(body) + "]")
			mutations = append(mutations, "download-client")
			writer.WriteHeader(http.StatusCreated)
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	})

	client := New("http://sonarr:8989", "fixture-api-key", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	changed, err := client.ReconcileSeriesLibrary(context.Background(), "fixture-qb-password")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first reconciliation reported no changes")
	}
	if want := []string{"root-folder", "download-client"}; !reflect.DeepEqual(mutations, want) {
		t.Fatalf("mutations = %v, want %v", mutations, want)
	}

	mutations = nil
	changed, err = client.ReconcileSeriesLibrary(context.Background(), "fixture-qb-password")
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(mutations) != 0 {
		t.Fatalf("repeated reconciliation changed state: changed=%v mutations=%v", changed, mutations)
	}
}

func TestAcquireLegalEpisodeUsesSupportedSonarrAPIs(t *testing.T) {
	seriesAdded := false
	releaseGrabbed := false
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-api-key" {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v3/series":
			if request.URL.Query().Get("tvdbId") != "103354" {
				http.Error(writer, "unexpected series", http.StatusBadRequest)
				return
			}
			if seriesAdded {
				_, _ = io.WriteString(writer, `[{"id":42,"title":"The Lucy Show","tvdbId":103354}]`)
			} else {
				_, _ = io.WriteString(writer, `[]`)
			}
		case "GET /api/v3/series/lookup":
			if request.URL.Query().Get("term") != "tvdb:103354" {
				http.Error(writer, "unexpected lookup", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `[{"title":"The Lucy Show","tvdbId":103354,"seasons":[{"seasonNumber":1}]}]`)
		case "GET /api/v3/qualityprofile":
			_, _ = io.WriteString(writer, `[{"id":7,"name":"Fixture 1080p"}]`)
		case "POST /api/v3/series":
			var series map[string]any
			_ = json.NewDecoder(request.Body).Decode(&series)
			if series["tvdbId"] != float64(103354) || series["rootFolderPath"] != seriesLibraryRoot || series["qualityProfileId"] != float64(7) {
				http.Error(writer, "unexpected series declaration", http.StatusBadRequest)
				return
			}
			seriesAdded = true
			series["id"] = 42
			_ = json.NewEncoder(writer).Encode(series)
		case "GET /api/v3/episode":
			if request.URL.Query().Get("seriesId") != "42" {
				http.Error(writer, "unexpected series identity", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `[{"id":84,"seriesId":42,"seasonNumber":1,"episodeNumber":1,"title":"Lucy Waits Up for Chris"}]`)
		case "GET /api/v3/release":
			if request.URL.Query().Get("episodeId") != "84" {
				http.Error(writer, "unexpected episode", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `[{"guid":"fixture-release","title":"The Lucy Show S01E01","indexer":"Internet Archive","episodeId":84}]`)
		case "POST /api/v3/release":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"guid":"fixture-release"`) {
				http.Error(writer, "unexpected release", http.StatusBadRequest)
				return
			}
			releaseGrabbed = true
		default:
			http.NotFound(writer, request)
		}
	})

	client := New("http://sonarr:8989", "fixture-api-key", &http.Client{Transport: handlerTransport{handler: handler}})
	seriesID, episodeID, err := client.AcquireLegalEpisode(context.Background(), 103354, 1, 1, "The Lucy Show S01E01", "Internet Archive")
	if err != nil {
		t.Fatal(err)
	}
	if seriesID != 42 || episodeID != 84 || !releaseGrabbed {
		t.Fatalf("acquisition result = series %d episode %d grabbed %t", seriesID, episodeID, releaseGrabbed)
	}
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
