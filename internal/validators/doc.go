// Package validators provides reusable terraform-plugin-framework validators
// shared across CoreWeave provider resources and data sources.
//
// Validators here are resource-agnostic and exported so any resource package
// (e.g. coreweave/inference) can import them. They deliberately live outside
// internal/provider: that package registers the resources and therefore imports
// the resource packages, so a validator placed there could not be imported back
// by a resource without creating an import cycle. Provider-configuration-only
// validators that are private to the provider package remain in
// internal/provider/validators.go.
package validators
