package auth

import (
	"errors"
	"strings"
)

type Scope struct {
	Region      string `json:"region"`
	Environment string `json:"environment"`
	Stage       string `json:"stage"`
}

type ScopePattern Scope

func CompileScopePattern(region, environment, stage string) (ScopePattern, error) {
	values := []string{region, environment, stage}
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "*?[]") && value != "*" {
			return ScopePattern{}, errors.New("scope pattern segments must be exact values or the whole-segment wildcard *")
		}
	}
	return ScopePattern{Region: region, Environment: environment, Stage: stage}, nil
}

func (pattern ScopePattern) Matches(scope Scope) bool {
	return segmentMatches(pattern.Region, scope.Region) && segmentMatches(pattern.Environment, scope.Environment) && segmentMatches(pattern.Stage, scope.Stage)
}

func segmentMatches(pattern, value string) bool { return pattern == "*" || pattern == value }
