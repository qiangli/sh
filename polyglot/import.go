package polyglot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ImportRequest is the declarative input for a direct foreign package import.
// Alias is already resolved by the syntax layer, including any safe default.
type ImportRequest struct {
	Source, Language, Environment, Module, Alias string
	Environ                                      []string
}

// ImportPlan is an immutable description of a package import. Planning selects
// an environment but deliberately does not start a runtime or import Module.
type ImportPlan struct {
	ID                      string
	Language, Module, Alias string
	Environment             EnvironmentPlan
}

// Clone returns an independently owned copy of p.
func (p ImportPlan) Clone() ImportPlan {
	p.Environment = p.Environment.Clone()
	return p
}

// PlanImport selects the immutable environment for one direct import without
// executing that environment or probing the requested module.
func PlanImport(request ImportRequest) (ImportPlan, error) {
	language := canonicalLanguage(request.Language)
	if language != "python" {
		return ImportPlan{}, fmt.Errorf("polyglot: unsupported import language %q", language)
	}
	if strings.TrimSpace(request.Module) == "" || strings.TrimSpace(request.Alias) == "" {
		return ImportPlan{}, errors.New("polyglot: import module and alias are required")
	}
	environment, err := DiscoverEnvironment(EnvironmentRequest{
		Source: request.Source, Language: language, Name: request.Environment, Environ: request.Environ,
	})
	if err != nil {
		return ImportPlan{}, err
	}
	hash := sha256.Sum256([]byte(language + "\x00" + request.Module + "\x00" + request.Alias + "\x00" + environment.Fingerprint))
	return ImportPlan{
		ID: hex.EncodeToString(hash[:]), Language: language, Module: request.Module,
		Alias: request.Alias, Environment: environment.Clone(),
	}, nil
}
