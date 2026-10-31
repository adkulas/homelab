package engine

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adkulas/homelab/internal/companions"
)

// waitForRequestedEpisodeSearches observes Sonarr commands. Verification must
// never supply the searches whose ownership it is trying to prove.
func waitForRequestedEpisodeSearches(ctx context.Context, observer *companions.SonarrEvidence, seriesID, season int, numbers []int, since, deadline time.Time) error {
	for {
		episodes, err := observer.Episodes(ctx, seriesID)
		if err != nil {
			return err
		}
		wantedNumbers := map[int]bool{}
		for _, number := range numbers {
			wantedNumbers[number] = true
		}
		expected := map[int]bool{}
		allowed := map[int]bool{}
		for _, episode := range episodes {
			if episode.SeasonNumber == season && episode.Monitored && !episode.AirDate.IsZero() && !episode.AirDate.After(time.Now()) {
				allowed[episode.ID] = true
				if wantedNumbers[episode.EpisodeNumber] {
					expected[episode.ID] = false
				}
			}
		}
		if len(expected) != len(wantedNumbers) {
			return fmt.Errorf("requested fixture episodes must be monitored, aired, and present in Sonarr")
		}
		commands, err := observer.Commands(ctx)
		if err != nil {
			return err
		}
		for _, command := range commands {
			at := command.SubmittedAt()
			if at.Before(since) {
				continue
			}
			name := command.CommandName()
			if name == "MissingEpisodeSearch" || (name == "SeasonSearch" && command.Body.SeriesID == seriesID && command.Body.SeasonNumber == season) {
				return fmt.Errorf("request triggered a grouped Sonarr search instead of episode searches")
			}
			if name != "EpisodeSearch" {
				continue
			}
			for _, id := range command.Body.EpisodeIDs {
				known := false
				for _, episode := range episodes {
					if episode.ID == id {
						known = true
						break
					}
				}
				if known && !allowed[id] {
					return fmt.Errorf("Sonarr searched an unrequested, unmonitored or unaired episode")
				}
				if _, ok := expected[id]; ok {
					expected[id] = true
				}
			}
		}
		complete := true
		for _, searched := range expected {
			complete = complete && searched
		}
		if complete {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Houndarr EpisodeSearch commands; check its schedule, grace, cooldown, queue backpressure and hourly cap")
		}
		if !waitForMediaPoll(ctx, deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("timed out waiting for episode searches")
		}
	}
}

func sonarrEvidence(base, key string) *companions.SonarrEvidence {
	return companions.NewSonarrEvidence(base, key, &http.Client{Timeout: 10 * time.Second})
}
