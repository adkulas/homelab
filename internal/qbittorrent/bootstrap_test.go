package qbittorrent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBootstrapRetriesStartupThenInstallsAndVerifiesDeclaredCredentials(t *testing.T) {
	const temporaryPassword = "one-time-secret"
	declared := Credentials{Username: "household", Password: "stable-secret"}
	loginAttempts := 0
	installed := false
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/auth/login":
			loginAttempts++
			body, _ := io.ReadAll(request.Body)
			form := string(body)
			if strings.Contains(form, "password="+temporaryPassword) || installed && strings.Contains(form, "password="+declared.Password) {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(writer, "Fails.", http.StatusUnauthorized)
		case "/api/v2/app/version":
			_, _ = writer.Write([]byte("v5.1.2"))
		case "/api/v2/app/setPreferences":
			installed = true
		default:
			http.NotFound(writer, request)
		}
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: &connectionResetOnceTransport{next: handlerTransport{handler: handler}}})
	credentialReads := 0
	err := client.Bootstrap(context.Background(), declared, func(context.Context) (string, bool, error) {
		credentialReads++
		return temporaryPassword, true, nil
	}, 100*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if !installed || credentialReads != 1 {
		t.Fatalf("installed = %v, current credential reads = %d", installed, credentialReads)
	}
}

func TestBootstrapReportsCredentialDriftWithoutCurrentStartCredential(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/auth/login" {
			http.Error(writer, "Fails.", http.StatusUnauthorized)
			return
		}
		http.NotFound(writer, request)
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	err := client.Bootstrap(context.Background(), Credentials{Username: "household", Password: "wrong"}, func(context.Context) (string, bool, error) {
		return "", false, nil
	}, 5*time.Millisecond, time.Millisecond)
	if !errors.Is(err, ErrCredentialDrift) {
		t.Fatalf("Bootstrap() error = %v, want ErrCredentialDrift", err)
	}
}

func TestBootstrapReportsRejectedCurrentStartCredential(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/auth/login" {
			http.Error(writer, "Fails.", http.StatusUnauthorized)
			return
		}
		http.NotFound(writer, request)
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	err := client.Bootstrap(context.Background(), Credentials{Username: "household", Password: "stable"}, func(context.Context) (string, bool, error) {
		return "temporary", true, nil
	}, 20*time.Millisecond, time.Millisecond)
	if !errors.Is(err, ErrBootstrapCredentialRejected) {
		t.Fatalf("Bootstrap() error = %v, want ErrBootstrapCredentialRejected", err)
	}
	if strings.Contains(err.Error(), "stable") || strings.Contains(err.Error(), "temporary") {
		t.Fatalf("Bootstrap() exposed a credential: %v", err)
	}
}

func TestBootstrapReportsBoundedReadinessTimeout(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "starting", http.StatusServiceUnavailable)
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	err := client.Bootstrap(context.Background(), Credentials{Username: "household", Password: "stable"}, func(context.Context) (string, bool, error) {
		return "", false, nil
	}, 5*time.Millisecond, time.Millisecond)
	if !errors.Is(err, ErrReadinessTimeout) {
		t.Fatalf("Bootstrap() error = %v, want ErrReadinessTimeout", err)
	}
}

type connectionResetOnceTransport struct {
	next  http.RoundTripper
	reset bool
}

func (transport *connectionResetOnceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !transport.reset && request.URL.Path == "/api/v2/auth/login" {
		transport.reset = true
		return nil, syscall.ECONNRESET
	}
	return transport.next.RoundTrip(request)
}
