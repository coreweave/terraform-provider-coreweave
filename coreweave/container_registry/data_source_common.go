package containerregistry

import (
	"context"
	"fmt"
	"sort"
	"strings"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/v2/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"buf.build/go/protovalidate"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// observedNamespaceAttributes explicitly defines namespace discovery observations.
func observedNamespaceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{

		"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Canonical API resource name used as the Terraform identifier."},
		"name":                schema.StringAttribute{Computed: true, MarkdownDescription: "Namespace name used in the registry hostname."},
		"dns_name":            schema.StringAttribute{Computed: true, MarkdownDescription: "Registry hostname used for OCI pushes and pulls."},
		"org_id":              schema.StringAttribute{Computed: true, MarkdownDescription: "Organization that owns the namespace."},
		"status":              schema.StringAttribute{Computed: true, MarkdownDescription: "Current namespace provisioning state."},
		"etag":                schema.StringAttribute{Computed: true, MarkdownDescription: "Current concurrency token. Changes when the server updates the resource."},
		"created_at":          schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp in RFC3339 format."},
		"updated_at":          schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp in RFC3339 format."},
		"zone":                schema.StringAttribute{Computed: true, MarkdownDescription: "Upper-case zone in which the namespace was provisioned."},
		"storage_quota_bytes": schema.Int64Attribute{Computed: true, MarkdownDescription: "Current namespace storage ceiling in bytes. Null means no ceiling; zero disables pushes after evaluation."},
		"created_by":          schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Identity that created the namespace.", Attributes: actorDataAttributes()},
		"updated_by":          schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Identity that last updated the namespace.", Attributes: actorDataAttributes()},
		"access_mode": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Current effective namespace access mode.", Attributes: map[string]schema.Attribute{
			"mode":       schema.StringAttribute{Computed: true, MarkdownDescription: "Effective access mode reported by the server."},
			"reasons":    schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Server-reported reasons for the effective access mode."},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp in RFC3339 format."},
		}},
		"content_status": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Server-reported storage usage and content counts. These observations may lag content changes.", Attributes: map[string]schema.Attribute{
			"usage_bytes":      schema.NumberAttribute{Computed: true, MarkdownDescription: "Total stored content size in bytes."},
			"blob_count":       schema.NumberAttribute{Computed: true, MarkdownDescription: "Number of stored blobs."},
			"manifest_count":   schema.NumberAttribute{Computed: true, MarkdownDescription: "Number of stored manifests."},
			"repository_count": schema.NumberAttribute{Computed: true, MarkdownDescription: "Number of repositories."},
			"tag_count":        schema.NumberAttribute{Computed: true, MarkdownDescription: "Number of tags."},
			"updated_at":       schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp in RFC3339 format."},
		}},
	}
}

// actorDataAttributes defines identities observed by namespace discovery.
func actorDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"user_uid": schema.StringAttribute{Computed: true, MarkdownDescription: "CoreWeave user identifier."},
		"username": schema.StringAttribute{Computed: true, MarkdownDescription: "CoreWeave username."},
	}
}

// zoneFilters canonicalizes filters and rejects case-equivalent duplicates.
func zoneFilters(v types.Set) ([]string, error) {
	if !known(v) {
		return nil, fmt.Errorf("zone_names must be known")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range v.Elements() {
		s := strings.ToUpper(v.(types.String).ValueString())
		if !validZone(s) {
			return nil, fmt.Errorf("invalid zone %q", s)
		}
		if seen[s] {
			return nil, fmt.Errorf("case-equivalent duplicate zone %q", s)
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if err := protovalidate.Validate(&api.ListZonesRequest{Zones: out}); err != nil {
		return nil, err
	}
	return out, nil
}

// listNamespaces exhausts pages and rejects repeated tokens and identities.
func listNamespaces(ctx context.Context, c client.RegistryServiceClient) ([]*api.RegistryNamespace, error) {
	out := []*api.RegistryNamespace{}
	tokens := map[string]bool{}
	names := map[string]bool{}
	token := ""
	for {
		res, e := c.ListRegistryNamespaces(ctx, &api.ListRegistryNamespacesRequest{PageSize: 200, PageToken: token})
		if e != nil {
			return nil, e
		}
		for _, n := range res.RegistryNamespaces {
			if names[n.Name] {
				return nil, fmt.Errorf("duplicate namespace %s across pages", n.Name)
			}
			names[n.Name] = true
			out = append(out, n)
		}
		token = res.NextPageToken
		if token == "" {
			break
		}
		if tokens[token] {
			return nil, fmt.Errorf("repeated namespace page token")
		}
		tokens[token] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
