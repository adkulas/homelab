package contractmodel

const SchemaVersion = "homelab.media-stack/configuration-contract/v1alpha1"

type Document struct {
	SchemaVersion string    `json:"schemaVersion"`
	Services      []Service `json:"services"`
}

type Service struct {
	Name      string    `json:"name"`
	Settings  []Setting `json:"settings"`
	Unmanaged string    `json:"unmanaged"`
}

type Setting struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Control        Control     `json:"control"`
	Description    string      `json:"description"`
	Source         string      `json:"source,omitempty"`
	Owner          string      `json:"owner,omitempty"`
	Type           string      `json:"type,omitempty"`
	AllowedValues  []string    `json:"allowedValues,omitempty"`
	Default        any         `json:"default,omitempty"`
	Sensitive      bool        `json:"sensitive"`
	Lifecycle      []Lifecycle `json:"lifecycle"`
	Status         Status      `json:"status"`
	OperatorChange string      `json:"operatorChange"`
}
