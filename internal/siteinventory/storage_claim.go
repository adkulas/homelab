package siteinventory

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

type storageRootClaim struct {
	exact     string
	ancestors map[string]bool
}

func storageClaim(path string) storageRootClaim {
	if path == "" {
		return storageRootClaim{}
	}
	clean := filepath.Clean(path)
	claim := storageRootClaim{exact: hashPath(clean), ancestors: map[string]bool{}}
	for parent := filepath.Dir(clean); parent != string(filepath.Separator) && parent != "."; parent = filepath.Dir(parent) {
		claim.ancestors[hashPath(parent)] = true
	}
	return claim
}

func declaredStorageClaim(exact string, ancestors []string) storageRootClaim {
	claim := storageRootClaim{exact: exact, ancestors: map[string]bool{}}
	for _, ancestor := range ancestors {
		claim.ancestors[ancestor] = true
	}
	return claim
}

func claimsOverlap(first, second storageRootClaim) bool {
	return first.exact == second.exact || first.ancestors[second.exact] || second.ancestors[first.exact]
}

func claimsMatch(path string, declared storageRootClaim) bool {
	actual := storageClaim(path)
	if actual.exact != declared.exact || len(actual.ancestors) != len(declared.ancestors) {
		return false
	}
	for ancestor := range actual.ancestors {
		if !declared.ancestors[ancestor] {
			return false
		}
	}
	return true
}

func hashPath(path string) string {
	digest := sha256.Sum256([]byte(path))
	return fmt.Sprintf("sha256:%x", digest)
}
