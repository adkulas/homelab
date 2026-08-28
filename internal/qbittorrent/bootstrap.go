package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrCredentialDrift               = errors.New("declared qBittorrent credentials were rejected and no current-start bootstrap credential was published")
	ErrBootstrapCredentialRejected   = errors.New("qBittorrent rejected its current-start bootstrap credential")
	ErrReadinessTimeout              = errors.New("qBittorrent API readiness timed out")
	ErrCurrentStartCredentialMissing = errors.New("qBittorrent current start reported temporary bootstrap mode without publishing a credential")
)

type TemporaryCredential func(context.Context) (password string, available bool, err error)

func (client *Client) Bootstrap(ctx context.Context, declared Credentials, current TemporaryCredential, timeout, retryInterval time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	sawDeclaredRejection := false
	for {
		err := client.Login(ctx, declared.Username, declared.Password)
		if err == nil {
			if err := client.ObserveProtectedAPI(ctx); err != nil {
				return fmt.Errorf("%w after declared login: %v", ErrProtectedObservation, err)
			}
			return nil
		}
		if errors.Is(err, ErrAuthenticationRejected) {
			sawDeclaredRejection = true
			password, available, observeErr := current(ctx)
			if observeErr != nil {
				return fmt.Errorf("observe current-start qBittorrent bootstrap credential: %w", observeErr)
			}
			if available {
				if err := client.Login(ctx, "admin", password); err != nil {
					if errors.Is(err, ErrAuthenticationRejected) {
						return fmt.Errorf("%w", ErrBootstrapCredentialRejected)
					}
				} else {
					if err := client.ReconcileWebUICredentials(ctx, declared); err != nil {
						return fmt.Errorf("install declared qBittorrent credentials: %w", err)
					}
					client.cookie = nil
					if err := client.Login(ctx, declared.Username, declared.Password); err != nil {
						return fmt.Errorf("reauthenticate with declared qBittorrent credentials: %w", err)
					}
					if err := client.ObserveProtectedAPI(ctx); err != nil {
						return fmt.Errorf("%w after installed credential login: %v", ErrProtectedObservation, err)
					}
					return nil
				}
			}
		}

		retry := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			retry.Stop()
			return ctx.Err()
		case <-deadline.C:
			retry.Stop()
			if sawDeclaredRejection {
				return fmt.Errorf("%w", ErrCredentialDrift)
			}
			return fmt.Errorf("%w after %s", ErrReadinessTimeout, timeout)
		case <-retry.C:
		}
	}
}
