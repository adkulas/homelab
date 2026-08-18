package sonarr

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"reflect"
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
