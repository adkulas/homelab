package companions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchObservation contains only safe operational fields from Houndarr's
// authenticated logs. Raw messages and labels are deliberately not returned.
type SearchObservation struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	ItemID    int    `json:"item_id"`
	ItemType  string `json:"item_type"`
}

func (client *Houndarr) ObserveSearches(ctx context.Context) ([]SearchObservation, error) {
	body, _, err := client.page(ctx, "GET", "/api/logs?limit=200&search_kind=missing", nil)
	if err != nil {
		return nil, err
	}
	var rows []SearchObservation
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		return nil, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check Houndarr's operational logs endpoint.")
	}
	return rows, nil
}

// QueueObservation is a supported Cleanuparr event, without free-text messages,
// titles, API targets or credentials.
type QueueObservation struct {
	Timestamp        time.Time `json:"timestamp"`
	EventType        string    `json:"eventType"`
	ItemHash         string    `json:"itemHash"`
	StrikeCount      int       `json:"strikeCount"`
	IsDryRun         bool      `json:"isDryRun"`
	DeleteReason     string    `json:"deleteReason"`
	RemoveFromClient bool      `json:"removeFromClient"`
	SearchType       string    `json:"searchType"`
	SearchStatus     string    `json:"searchStatus"`
}

func (client *Cleanuparr) ObserveQueue(ctx context.Context, since time.Time) ([]QueueObservation, error) {
	var result []QueueObservation
	for page := 1; page <= 10; page++ {
		var response struct {
			Items      []QueueObservation `json:"items"`
			TotalPages int                `json:"totalPages"`
		}
		path := fmt.Sprintf("/api/events?page=%d&pageSize=100&fromDate=%s", page, url.QueryEscape(since.Format(time.RFC3339)))
		if err := client.api.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Items...)
		if page >= response.TotalPages {
			return result, nil
		}
	}
	return nil, fail("VERIFY_COMPANION_EVIDENCE_TRUNCATED", "More than 1000 recent queue events require a narrower disposable verification window.")
}

// SonarrEvidence observes supported Sonarr APIs and never starts searches.
type SonarrEvidence struct{ api api }

func NewSonarrEvidence(base, key string, client *http.Client) *SonarrEvidence {
	return &SonarrEvidence{api: newAPI(base, key, client)}
}

type SearchCommand struct {
	Name    string    `json:"name"`
	Started time.Time `json:"startedOn"`
	Queued  time.Time `json:"queued"`
	Body    struct {
		Name         string `json:"name"`
		EpisodeIDs   []int  `json:"episodeIds"`
		SeriesID     int    `json:"seriesId"`
		SeasonNumber int    `json:"seasonNumber"`
	} `json:"body"`
}
type Episode struct {
	ID            int       `json:"id"`
	SeriesID      int       `json:"seriesId"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeNumber int       `json:"episodeNumber"`
	Monitored     bool      `json:"monitored"`
	HasFile       bool      `json:"hasFile"`
	AirDate       time.Time `json:"airDateUtc"`
}

func (client *SonarrEvidence) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var rows []Episode
	err := client.api.request(ctx, "GET", fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), nil, &rows)
	return rows, err
}
func (client *SonarrEvidence) Commands(ctx context.Context) ([]SearchCommand, error) {
	var rows []SearchCommand
	err := client.api.request(ctx, "GET", "/api/v3/command", nil, &rows)
	return rows, err
}
func (client *SonarrEvidence) DownloadState(ctx context.Context, hash string) (queued, blocked bool, err error) {
	for _, kind := range []string{"queue", "blocklist"} {
		found := false
		for page := 1; page <= 10; page++ {
			var response struct {
				Records []struct {
					DownloadID string `json:"downloadId"`
				} `json:"records"`
				TotalRecords int `json:"totalRecords"`
			}
			path := fmt.Sprintf("/api/v3/%s?page=%d&pageSize=100", kind, page)
			if err := client.api.request(ctx, "GET", path, nil, &response); err != nil {
				return false, false, err
			}
			for _, record := range response.Records {
				if strings.EqualFold(record.DownloadID, hash) {
					found = true
				}
			}
			if page*100 >= response.TotalRecords {
				break
			}
			if page == 10 {
				return false, false, fail("VERIFY_COMPANION_EVIDENCE_TRUNCATED", "Sonarr queue or blocklist exceeds the bounded observation window.")
			}
		}
		if kind == "queue" {
			queued = found
		} else {
			blocked = found
		}
	}
	return queued, blocked, nil
}

// SubmittedAt normalizes Sonarr versions that publish only a start timestamp.
func (command SearchCommand) SubmittedAt() time.Time {
	if !command.Queued.IsZero() {
		return command.Queued
	}
	return command.Started
}

// CommandName accepts either the command envelope or its submitted body.
func (command SearchCommand) CommandName() string {
	if command.Name != "" {
		return command.Name
	}
	return command.Body.Name
}
