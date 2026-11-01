package companions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Failure exposes stable diagnostics without exposing upstream bodies or secrets.
type Failure struct {
	Code, Remedy string
	cause        error
	status       int
}

func (failure *Failure) Error() string { return failure.Code + ": " + failure.Remedy }
func (failure *Failure) Unwrap() error { return failure.cause }
func fail(code, remedy string) error   { return &Failure{Code: code, Remedy: remedy} }

type document map[string]any

type api struct {
	base, key string
	bearer    string
	http      *http.Client
}

func newAPI(base, key string, client *http.Client) api {
	copyClient := *client
	copyClient.Timeout = 10 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return api{base: base, key: key, http: &copyClient}
}
func (client api) request(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.base+path, reader)
	if err != nil {
		return fail("VERIFY_COMPANION_UNAVAILABLE", "Check the companion management address.")
	}
	if client.key != "" {
		request.Header.Set("X-Api-Key", client.key)
	} else if client.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+client.bearer)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return &Failure{Code: "VERIFY_COMPANION_UNAVAILABLE", Remedy: "Check companion availability and retry.", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Correct the selected Environment companion credential and retry.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Failure{Code: "VERIFY_COMPANION_API_FAILED", Remedy: fmt.Sprintf("Companion API %s %s returned HTTP %d; inspect its local logs.", method, path, response.StatusCode), status: response.StatusCode}
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result); err != nil {
			return fail("VERIFY_COMPANION_RESPONSE_INVALID", "Companion returned an invalid configuration response; check the pinned version.")
		}
	}
	return nil
}

func same(value, desired any) bool {
	a, _ := json.Marshal(value)
	b, _ := json.Marshal(desired)
	return bytes.Equal(a, b)
}
func includes(current, desired document) bool {
	for key, value := range desired {
		if nested, ok := value.(document); ok {
			observed, ok := current[key].(map[string]any)
			if !ok || !includes(observed, nested) {
				return false
			}
		} else if !same(current[key], value) {
			return false
		}
	}
	return true
}
func merge(current, desired document) document {
	out := document{}
	for key, value := range current {
		out[key] = value
	}
	for key, value := range desired {
		out[key] = value
	}
	return out
}
