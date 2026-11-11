package engine

// DestroyResourceKind limits artifacts and execution to explicitly owned resource categories.
type DestroyResourceKind string

const (
	DestroyContainer        DestroyResourceKind = "container"
	DestroyNetwork          DestroyResourceKind = "network"
	DestroyRuntimeFile      DestroyResourceKind = "runtime"
	DestroyVolume           DestroyResourceKind = "volume"
	DestroyDownloads        DestroyResourceKind = "downloads"
	DestroyMedia            DestroyResourceKind = "media"
	DestroySecrets          DestroyResourceKind = "secrets"
	DestroyDrillCredentials DestroyResourceKind = "drill-credentials"
)

func (kind DestroyResourceKind) order() int {
	switch kind {
	case DestroyContainer:
		return 0
	case DestroyNetwork:
		return 1
	case DestroyRuntimeFile:
		return 2
	case DestroyVolume:
		return 3
	case DestroyDownloads:
		return 4
	case DestroyMedia:
		return 5
	case DestroySecrets:
		return 6
	case DestroyDrillCredentials:
		return 7
	default:
		return -1
	}
}

type destroyPath struct {
	kind DestroyResourceKind
	path string
}
