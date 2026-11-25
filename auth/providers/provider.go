package providers

import "context"

// Provider retrieves CoreWeave bearer credentials and releases resources.
type Provider interface {
	Retrieve(context.Context) (Credential, error)
	Close()
}
