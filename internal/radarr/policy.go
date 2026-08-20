package radarr

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const moviePolicySchemaVersion = "homelab.media-stack.policy/v1alpha1"

type MoviePolicy struct {
	APIVersion         string                   `yaml:"apiVersion"`
	Kind               string                   `yaml:"kind"`
	Source             MoviePolicySource        `yaml:"source"`
	Profile            MovieQualityProfile      `yaml:"profile"`
	QualityDefinitions []MovieQualityDefinition `yaml:"qualityDefinitions"`
	Naming             MovieNamingPolicy        `yaml:"naming"`
	MediaManagement    MovieMediaManagement     `yaml:"mediaManagement"`
	Deviations         []string                 `yaml:"deviations"`
}

type MoviePolicySource struct {
	Repository      string `yaml:"repository"`
	Revision        string `yaml:"revision"`
	ManifestVersion string `yaml:"manifestVersion"`
}

type MovieQualityProfile struct {
	Name                          string              `yaml:"name"`
	UpgradesAllowed               bool                `yaml:"upgradesAllowed"`
	UpgradeUntilQuality           string              `yaml:"upgradeUntilQuality"`
	MinimumCustomFormatScore      int                 `yaml:"minimumCustomFormatScore"`
	UpgradeUntilCustomFormatScore int                 `yaml:"upgradeUntilCustomFormatScore"`
	AllowedQualities              []string            `yaml:"allowedQualities"`
	CustomFormats                 []MovieCustomFormat `yaml:"customFormats"`
}

type MovieCustomFormat struct {
	Name  string `yaml:"name"`
	Score int    `yaml:"score"`
}

type MovieQualityDefinition struct {
	Quality   string `yaml:"quality"`
	Minimum   int    `yaml:"minimum"`
	Maximum   int    `yaml:"maximum"`
	Preferred int    `yaml:"preferred"`
}

type MovieNamingPolicy struct {
	Preset                   string `yaml:"preset"`
	RenameMovies             bool   `yaml:"renameMovies"`
	StandardMovieFormat      string `yaml:"standardMovieFormat"`
	MovieFolderFormat        string `yaml:"movieFolderFormat"`
	ReplaceIllegalCharacters bool   `yaml:"replaceIllegalCharacters"`
	ColonReplacementFormat   string `yaml:"colonReplacementFormat"`
}

type MovieMediaManagement struct {
	Preset                    string `yaml:"preset"`
	DownloadPropersAndRepacks string `yaml:"downloadPropersAndRepacks"`
	EnableMediaInfo           bool   `yaml:"enableMediaInfo"`
}

var gitRevision = regexp.MustCompile(`^[a-f0-9]{40}$`)

func LoadMoviePolicy(path, pinnedRevision string) (MoviePolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return MoviePolicy{}, fmt.Errorf("load Movie Library policy fixture: %w", err)
	}
	defer file.Close()
	var policy MoviePolicy
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return MoviePolicy{}, fmt.Errorf("load Movie Library policy fixture: %w", err)
	}
	if policy.APIVersion != moviePolicySchemaVersion || policy.Kind != "ProfilarrMoviePolicy" {
		return MoviePolicy{}, fmt.Errorf("load Movie Library policy fixture: unsupported document %q %q", policy.APIVersion, policy.Kind)
	}
	if !gitRevision.MatchString(pinnedRevision) || policy.Source.Revision != pinnedRevision {
		return MoviePolicy{}, fmt.Errorf("Movie Library policy fixture revision %q does not match pinned Profilarr PCD revision %q", policy.Source.Revision, pinnedRevision)
	}
	if policy.Source.Repository == "" || policy.Profile.Name == "" || len(policy.Profile.AllowedQualities) == 0 {
		return MoviePolicy{}, fmt.Errorf("Movie Library policy fixture requires source repository, profile, and allowed qualities")
	}
	return policy, nil
}

type observedQualityProfile struct {
	Name              string                       `json:"name"`
	UpgradeAllowed    bool                         `json:"upgradeAllowed"`
	Cutoff            int                          `json:"cutoff"`
	MinFormatScore    int                          `json:"minFormatScore"`
	CutoffFormatScore int                          `json:"cutoffFormatScore"`
	Items             []observedQualityProfileItem `json:"items"`
	FormatItems       []struct {
		Format int `json:"format"`
		Score  int `json:"score"`
	} `json:"formatItems"`
}

type observedQualityProfileItem struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Allowed bool   `json:"allowed"`
	Quality *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"quality"`
	Items []observedQualityProfileItem `json:"items"`
}

type observedCustomFormat struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type observedQualityDefinition struct {
	Quality struct {
		Name string `json:"name"`
	} `json:"quality"`
	MinSize       int `json:"minSize"`
	MaxSize       int `json:"maxSize"`
	PreferredSize int `json:"preferredSize"`
}

func (client *Client) VerifyMoviePolicy(ctx context.Context, policy MoviePolicy) error {
	var profiles []observedQualityProfile
	var formats []observedCustomFormat
	var definitions []observedQualityDefinition
	var naming MovieNamingPolicy
	var media MovieMediaManagement
	for _, observation := range []struct {
		endpoint string
		target   any
	}{
		{"/api/v3/qualityprofile", &profiles},
		{"/api/v3/customformat", &formats},
		{"/api/v3/qualitydefinition", &definitions},
		{"/api/v3/config/naming", &naming},
		{"/api/v3/config/mediamanagement", &media},
	} {
		if err := client.getJSON(ctx, observation.endpoint, observation.target); err != nil {
			return err
		}
	}

	var drift []string
	var profile *observedQualityProfile
	for index := range profiles {
		if profiles[index].Name == policy.Profile.Name {
			profile = &profiles[index]
			break
		}
	}
	if profile == nil {
		drift = append(drift, fmt.Sprintf("quality profile %q is missing", policy.Profile.Name))
	} else {
		if profile.UpgradeAllowed != policy.Profile.UpgradesAllowed ||
			profile.MinFormatScore != policy.Profile.MinimumCustomFormatScore ||
			profile.CutoffFormatScore != policy.Profile.UpgradeUntilCustomFormatScore {
			drift = append(drift, fmt.Sprintf("quality profile %q upgrade contract differs", policy.Profile.Name))
		}
		if cutoffQuality(profile.Items, profile.Cutoff) != policy.Profile.UpgradeUntilQuality {
			drift = append(drift, fmt.Sprintf("quality profile %q cutoff differs", policy.Profile.Name))
		}
		if got, want := allowedQualityNames(profile.Items), sortedCopy(policy.Profile.AllowedQualities); !reflect.DeepEqual(got, want) {
			drift = append(drift, fmt.Sprintf("quality profile %q allowed qualities differ", policy.Profile.Name))
		}
		formatNames := make(map[int]string, len(formats))
		for _, format := range formats {
			formatNames[format.ID] = format.Name
		}
		scores := make(map[string]int, len(profile.FormatItems))
		for _, item := range profile.FormatItems {
			scores[formatNames[item.Format]] = item.Score
		}
		for _, expected := range policy.Profile.CustomFormats {
			if score, ok := scores[expected.Name]; !ok || score != expected.Score {
				drift = append(drift, fmt.Sprintf("custom format %q score differs", expected.Name))
			}
		}
	}

	definitionsByName := make(map[string]observedQualityDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByName[definition.Quality.Name] = definition
	}
	for _, expected := range policy.QualityDefinitions {
		observed, ok := definitionsByName[expected.Quality]
		if !ok || observed.MinSize != expected.Minimum || observed.MaxSize != expected.Maximum || observed.PreferredSize != expected.Preferred {
			drift = append(drift, fmt.Sprintf("quality definition %q differs", expected.Quality))
		}
	}
	if naming.RenameMovies != policy.Naming.RenameMovies ||
		naming.StandardMovieFormat != policy.Naming.StandardMovieFormat ||
		naming.MovieFolderFormat != policy.Naming.MovieFolderFormat ||
		naming.ReplaceIllegalCharacters != policy.Naming.ReplaceIllegalCharacters ||
		naming.ColonReplacementFormat != policy.Naming.ColonReplacementFormat {
		drift = append(drift, fmt.Sprintf("naming preset %q differs", policy.Naming.Preset))
	}
	if media.DownloadPropersAndRepacks != policy.MediaManagement.DownloadPropersAndRepacks ||
		media.EnableMediaInfo != policy.MediaManagement.EnableMediaInfo {
		drift = append(drift, fmt.Sprintf("media-management preset %q differs", policy.MediaManagement.Preset))
	}
	if len(drift) > 0 {
		return fmt.Errorf("Movie Library policy drift: %s", strings.Join(drift, "; "))
	}
	return nil
}

func allowedQualityNames(items []observedQualityProfileItem) []string {
	var names []string
	for _, item := range items {
		if !item.Allowed {
			continue
		}
		if item.Quality != nil {
			names = append(names, item.Quality.Name)
		} else if item.Name != "" {
			names = append(names, item.Name)
		}
	}
	sort.Strings(names)
	return names
}

func cutoffQuality(items []observedQualityProfileItem, cutoff int) string {
	for _, item := range items {
		if item.Quality != nil && item.Quality.ID == cutoff {
			return item.Quality.Name
		}
		if name := cutoffQuality(item.Items, cutoff); name != "" {
			return name
		}
	}
	return ""
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
