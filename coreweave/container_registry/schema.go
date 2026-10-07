// Package containerregistry manages Container Registry through the public central API.
package containerregistry

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"sync"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/internal/validators"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// validateProtoField uses the published annotation for a single identifier or zone.
func validateProtoField(message proto.Message, name protoreflect.Name) error {
	field := message.ProtoReflect().Descriptor().Fields().ByName(name)
	return protovalidate.Validate(message, protovalidate.WithFilter(protovalidate.FilterFunc(func(_ protoreflect.Message, d protoreflect.Descriptor) bool { return d == field })))
}

// validParent uses the canonical parent constraint from the public request contract.
func validParent(s string) bool {
	return protovalidate.Validate(&api.GetRegistryAccessConfigurationRequest{Parent: s}) == nil
}

// validNamespace reuses the public namespace identifier annotation for imports and operation names.
func validNamespace(s string) bool {
	return validateProtoField(&api.CreateRegistryNamespaceRequest{RegistryNamespaceId: s}, "registry_namespace_id") == nil
}

// validZone retains Terraform's uppercase spelling while reusing API zone validation.
func validZone(s string) bool {
	return s == strings.ToUpper(s) && validateProtoField(&api.RegistryNamespace{Zone: s}, "zone") == nil
}

// validExpression rejects whitespace-only expressions, which has no proto annotation.
func validExpression(s string) bool { return strings.TrimSpace(s) != "" }

// validCIDR accepts only canonical prefixes without host bits or IPv4-mapped IPv6.
func validCIDR(s string) bool {
	p, e := netip.ParsePrefix(s)
	return e == nil && !p.Addr().Is4In6() && p == p.Masked() && s == p.String()
}

type parentNameValidator struct{}

// Description describes the parent constraint.
func (parentNameValidator) Description(context.Context) string {
	return "Valid Container Registry parent"
}

// MarkdownDescription describes the parent constraint.
func (v parentNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString validates known parent values and defers unresolved configuration.
func (parentNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if !(validParent(s)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid parent", fmt.Sprintf("%q is not a valid canonical parent.", s))
	}
}

type expressionValidator struct{}

// Description describes the expression constraint.
func (expressionValidator) Description(context.Context) string {
	return "Valid Container Registry expression"
}

// MarkdownDescription describes the expression constraint.
func (v expressionValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString validates known expression values and defers unresolved configuration.
func (expressionValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if !(validExpression(s)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid expression", fmt.Sprintf("%q is not a valid canonical expression.", s))
	}
}

type canonicalCIDRValidator struct{}

// Description describes the CIDR constraint.
func (canonicalCIDRValidator) Description(context.Context) string {
	return "Valid Container Registry CIDR"
}

// MarkdownDescription describes the CIDR constraint.
func (v canonicalCIDRValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString validates known CIDR values and defers unresolved configuration.
func (canonicalCIDRValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if !(validCIDR(s)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", fmt.Sprintf("%q is not a valid canonical CIDR.", s))
	}
}

type canonicalDurationValidator struct{}

// Description describes the duration constraint.
func (canonicalDurationValidator) Description(context.Context) string {
	return "Valid Container Registry duration"
}

// MarkdownDescription describes the duration constraint.
func (v canonicalDurationValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString validates known duration values and defers unresolved configuration.
func (canonicalDurationValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if !(validAge(s)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration", fmt.Sprintf("%q is not a valid canonical duration.", s))
	}
}

type selectorValidator struct{}

// Description describes the selector constraint.
func (selectorValidator) Description(context.Context) string {
	return "Valid Container Registry selector"
}

// MarkdownDescription describes the selector constraint.
func (v selectorValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

// ValidateString validates known selector values and defers unresolved configuration.
func (selectorValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	enumRequest := req
	enumRequest.ConfigValue = types.StringValue("REGISTRY_IDENTITY_SELECTOR_" + s)
	validators.DeprecatedEnumValue(api.RegistryIdentitySelector(0).Descriptor()).ValidateString(ctx, enumRequest, resp)
	if !(validSelector(s)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid selector", fmt.Sprintf("%q is not a valid canonical selector.", s))
	}
}

// validAge checks the canonical protobuf duration contract used by the API.
func validAge(s string) bool { _, err := parseAge(s); return err == nil }

// requiredString defines a string input with explicit constraints.
func requiredString(description string, replace bool, checks ...validator.String) schema.StringAttribute {
	a := schema.StringAttribute{Required: true, MarkdownDescription: description, Validators: checks}
	if replace {
		a.PlanModifiers = []planmodifier.String{stringplanmodifier.RequiresReplace()}
	}
	return a
}

// namespaceTypes caches the namespace schema's immutable attribute types.
var namespaceTypes = sync.OnceValue(func() map[string]attr.Type { return typesOf(namespaceAttributes()) })

// accessTypes caches the access schema's immutable attribute types.
var accessTypes = sync.OnceValue(func() map[string]attr.Type { return typesOf(accessResourceAttributes()) })

// lifecycleTypes caches the lifecycle schema's immutable attribute types.
var lifecycleTypes = sync.OnceValue(func() map[string]attr.Type { return typesOf(lifecycleResourceAttributes()) })

// typesOf returns nested object types from their schema.
func typesOf(a map[string]schema.Attribute) map[string]attr.Type {
	t := map[string]attr.Type{}
	for k, v := range a {
		t[k] = v.GetType()
	}
	return t
}

// accessAttributes defines content shared by bootstrap and ongoing ownership.
func accessAttributes() map[string]schema.Attribute {
	rule := map[string]schema.Attribute{"expression": requiredString("CEL expression evaluated against the request identity and context.", false, expressionValidator{})}
	policy := map[string]schema.Attribute{"identity_selector": requiredString(selectorDescription(), false, selectorValidator{}), "rules": schema.MapNestedAttribute{Required: true, NestedObject: schema.NestedAttributeObject{Attributes: rule}, MarkdownDescription: "Authoritative rules keyed by stable IDs. Omitted rules are removed."}}
	acl := map[string]schema.Attribute{}
	for _, k := range []string{"allow_cidrs", "deny_cidrs"} {
		acl[k] = schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})), Validators: []validator.Set{setvalidator.ValueStringsAre(canonicalCIDRValidator{})}, MarkdownDescription: "Canonical IPv4 or IPv6 networks; host bits and IPv4-mapped IPv6 are rejected."}
	}
	return map[string]schema.Attribute{
		"policy_sets":    schema.MapNestedAttribute{Optional: true, Computed: true, Default: mapdefault.StaticValue(types.MapValueMust(types.ObjectType{AttrTypes: typesOf(policy)}, map[string]attr.Value{})), NestedObject: schema.NestedAttributeObject{Attributes: policy}, MarkdownDescription: "Authoritative policy sets keyed by stable IDs. Omitted entries are removed. COREWEAVE_KUBERNETES is deprecated; use WORKLOAD_FEDERATION."},
		"request_ip_acl": schema.SingleNestedAttribute{Optional: true, Attributes: acl, MarkdownDescription: "Optional request filter. At least one allow or deny network is required; omission removes the ACL. An ACL grants no authorization."},
	}
}

// namespaceAttributes defines API observations and namespace inputs.
func namespaceAttributes() map[string]schema.Attribute {
	a := map[string]schema.Attribute{
		"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Terraform identifier for this resource.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":                schema.StringAttribute{Computed: true, MarkdownDescription: "Canonical API resource name.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"dns_name":            schema.StringAttribute{Computed: true, MarkdownDescription: "Registry hostname used for OCI pushes and pulls.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"org_id":              schema.StringAttribute{Computed: true, MarkdownDescription: "Organization that owns the namespace."},
		"status":              schema.StringAttribute{Computed: true, MarkdownDescription: "Current namespace provisioning state."},
		"etag":                schema.StringAttribute{Computed: true, MarkdownDescription: "Current concurrency token. Changes when the server updates the resource."},
		"created_at":          schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp in RFC3339 format."},
		"updated_at":          schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp in RFC3339 format."},
		"namespace_id":        requiredString("Namespace identifier used in the registry hostname. Changing it replaces the namespace.", true),
		"zone":                requiredString("Upper-case zone identifier. Changing it replaces the namespace.", true, stringvalidator.RegexMatches(regexp.MustCompile(`^[^a-z]*$`), "Must use an upper-case zone")),
		"storage_quota_bytes": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(0)}, MarkdownDescription: "Namespace ceiling in bytes. Omission/null clears the ceiling; zero disables pushes after evaluation."},
		"created_by":          schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Identity that created the namespace.", Attributes: actorAttributes()},
		"updated_by":          schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "Identity that last updated the namespace.", Attributes: actorAttributes()},
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
		"force_destroy":                schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Irreversibly delete content on destroy. Apply this setting before destroying; never enabled automatically."},
		"initial_access_configuration": schema.SingleNestedAttribute{Optional: true, Attributes: accessAttributes(), PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()}, MarkdownDescription: "Historical create-only input. Omission selects the server default; {} atomically installs deny-all. Any change replaces the namespace. Import leaves this null; adding it after import replaces the namespace."},
		"timeouts":                     timeouts.AttributesAll(context.Background()),
	}
	return a
}

// actorAttributes defines the observed request identity.
func actorAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"user_uid": schema.StringAttribute{Computed: true, MarkdownDescription: "CoreWeave user identifier."},
		"username": schema.StringAttribute{Computed: true, MarkdownDescription: "CoreWeave username."},
	}
}

// policyMetadataAttributes defines common singleton identity and observations.
func policyMetadataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"namespace":  requiredString("Canonical parent namespace name. Changing it replaces this policy resource.", true, parentNameValidator{}),
		"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Terraform identifier for this resource.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "Canonical API resource name.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"etag":       schema.StringAttribute{Computed: true, MarkdownDescription: "Current concurrency token. Changes when the server updates the resource."},
		"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp in RFC3339 format."},
		"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp in RFC3339 format."},
		"revision":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Current desired policy revision."},
		"timeouts":   timeouts.AttributesAll(context.Background()),
	}
}

// accessResourceAttributes defines the authoritative access configuration.
func accessResourceAttributes() map[string]schema.Attribute {
	a := policyMetadataAttributes()
	for name, attribute := range accessAttributes() {
		a[name] = attribute
	}
	a["access_config_state"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Current access policy rollout state."}
	return a
}

// lifecycleResourceAttributes defines the authoritative retention configuration.
func lifecycleResourceAttributes() map[string]schema.Attribute {
	a := policyMetadataAttributes()
	a["enabled"] = schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Whether lifecycle retention is enabled. Defaults to false."}
	rule := map[string]schema.Attribute{
		"regex":       requiredString("Go RE2 expression matched against the complete repository name or repository:tag. Matching is anchored to the whole input.", false, regexpValidator{}),
		"older_than":  schema.StringAttribute{Optional: true, MarkdownDescription: "Canonical positive protobuf duration, such as 86400s.", Validators: []validator.String{canonicalDurationValidator{}}},
		"keep_newest": schema.Int64Attribute{Optional: true, MarkdownDescription: "Number of newest matching images to retain, from 1 to 100."},
	}
	a["rules"] = schema.MapNestedAttribute{Optional: true, Computed: true, MarkdownDescription: "Authoritative rules keyed by stable IDs. Omitted rules are removed.", NestedObject: schema.NestedAttributeObject{Attributes: rule}, Default: mapdefault.StaticValue(types.MapValueMust(types.ObjectType{AttrTypes: typesOf(rule)}, map[string]attr.Value{}))}
	a["applied_revision"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "Last lifecycle policy revision acknowledged by the worker. Acknowledgement does not indicate completed retention or garbage collection."}
	return a
}

// regexpValidator validates the syntax of a user-supplied repository pattern.
type regexpValidator struct{}

// Description describes the regular expression constraint.
func (regexpValidator) Description(context.Context) string { return "Valid regular expression" }

// MarkdownDescription describes the regular expression constraint.
func (v regexpValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

// ValidateString defers unknown values and validates Go regular expression syntax.
func (regexpValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := regexp.Compile(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid regex", err.Error())
	}
}

// validSelector retains the deprecated API value for lossless imported round trips.
func validSelector(s string) bool {
	value := api.RegistryIdentitySelector(0).Descriptor().Values().ByName(protoreflect.Name("REGISTRY_IDENTITY_SELECTOR_" + s))
	return value != nil && value.Number() != 0
}

// selectorDescription derives supported identity classes from the published enum.
func selectorDescription() string {
	values := api.RegistryIdentitySelector(0).Descriptor().Values()
	names := make([]string, 0, values.Len()-1)
	for i := 0; i < values.Len(); i++ {
		value := values.Get(i)
		if value.Number() != 0 {
			names = append(names, "`"+strings.TrimPrefix(string(value.Name()), "REGISTRY_IDENTITY_SELECTOR_")+"`")
		}
	}
	return "Identity class evaluated by this policy set. Supported values: " + strings.Join(names, ", ") + ". COREWEAVE_KUBERNETES is deprecated; use WORKLOAD_FEDERATION."
}
