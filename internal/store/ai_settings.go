package store

import (
	"context"
	"regexp"
)

const stateAIDeployment = "ai_deployment"

var deploymentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,89}$`)

// AIDeployment is the global, non-secret model selection shared by all households.
func (s *Store) AIDeployment(ctx context.Context) (string, error) {
	return s.GetState(ctx, stateAIDeployment)
}

// SetAIDeployment stores a deployment name, or clears it when empty.
// The web layer separately verifies availability against the Azure catalog.
func (s *Store) SetAIDeployment(ctx context.Context, deployment string) error {
	if deployment != "" && !deploymentName.MatchString(deployment) {
		return ErrInvalid
	}
	return s.SetState(ctx, stateAIDeployment, deployment)
}
