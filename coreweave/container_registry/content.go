package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"time"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"buf.build/go/protovalidate"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// known recursively rejects unknown apply inputs rather than converting them to zero values.
func known(v attr.Value) bool {
	if v.IsUnknown() {
		return false
	}
	if v.IsNull() {
		return true
	}
	switch x := v.(type) {
	case types.Object:
		for _, e := range x.Attributes() {
			if !known(e) {
				return false
			}
		}
	case types.Map:
		for _, e := range x.Elements() {
			if !known(e) {
				return false
			}
		}
	case types.Set:
		for _, e := range x.Elements() {
			if !known(e) {
				return false
			}
		}
	}
	return true
}

// keys orders API repeated fields deterministically.
func keys(m map[string]attr.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseAge requires canonical protobuf JSON duration spelling and bounded server arithmetic.
func parseAge(s string) (*durationpb.Duration, error) {
	d := new(durationpb.Duration)
	if e := protojson.Unmarshal([]byte(fmt.Sprintf("%q", s)), d); e != nil {
		return nil, e
	}
	if e := d.CheckValid(); e != nil {
		return nil, e
	}
	n := new(big.Int).Mul(big.NewInt(d.Seconds), big.NewInt(1e9))
	n.Add(n, big.NewInt(int64(d.Nanos)))
	if n.Sign() <= 0 || !n.IsInt64() {
		return nil, fmt.Errorf("age must be positive and fit time.Duration")
	}
	b, _ := protojson.Marshal(d)
	if string(b) != fmt.Sprintf("%q", s) {
		return nil, fmt.Errorf("use canonical protobuf duration %s", b)
	}
	return d, nil
}

// AccessPolicySetModel preserves configured identity and nested rules.
type AccessPolicySetModel struct {
	IdentitySelector types.String `tfsdk:"identity_selector"`
	Rules            types.Map    `tfsdk:"rules"`
}

// AccessRuleModel preserves CEL source without normalization.
type AccessRuleModel struct {
	Expression types.String `tfsdk:"expression"`
}

// IPACLModel preserves the distinction between an omitted ACL and empty sets.
type IPACLModel struct {
	AllowCIDRs types.Set `tfsdk:"allow_cidrs"`
	DenyCIDRs  types.Set `tfsdk:"deny_cidrs"`
}

// AccessConfigurationModel represents both bootstrap and singleton policy content.
type AccessConfigurationModel struct {
	PolicySets   types.Map    `tfsdk:"policy_sets"`
	RequestIPACL types.Object `tfsdk:"request_ip_acl"`
}

// LifecycleRuleModel preserves optional conditions independently of their values.
type LifecycleRuleModel struct {
	Regex      types.String `tfsdk:"regex"`
	OlderThan  types.String `tfsdk:"older_than"`
	KeepNewest types.Int64  `tfsdk:"keep_newest"`
}

// diagnosticError carries Framework diagnostics through operation helpers without flattening them.
type diagnosticError struct{ diagnostics diag.Diagnostics }

// Error supplies context to callers that cannot consume Framework diagnostics directly.
func (e *diagnosticError) Error() string {
	messages := make([]error, 0, len(e.diagnostics.Errors()))
	for _, d := range e.diagnostics.Errors() {
		messages = append(messages, fmt.Errorf("%s: %s", d.Summary(), d.Detail()))
	}
	return errors.Join(messages...).Error()
}

// conversionError preserves diagnostic severity and attribute paths at the API-helper boundary.
func conversionError(d diag.Diagnostics) error {
	if !d.HasError() {
		return nil
	}
	return &diagnosticError{diagnostics: d}
}

// validateProto enforces annotated API constraints and accumulates every violation.
func validateProto(message proto.Message, d *diag.Diagnostics) {
	err := protovalidate.Validate(message)
	if err == nil {
		return
	}
	var validation *protovalidate.ValidationError
	if errors.As(err, &validation) {
		for _, violation := range validation.Violations {
			d.AddError("Invalid Container Registry configuration", violation.String())
		}
		return
	}
	d.AddError("Container Registry validation failed", err.Error())
}

// accessInput converts customer-controlled content and accumulates independent validation errors.
func accessInput(ctx context.Context, policies types.Map, acl types.Object, root path.Path) (*api.RegistryAccessConfiguration, diag.Diagnostics) {
	var d diag.Diagnostics
	out := &api.RegistryAccessConfiguration{}
	if !known(policies) || !known(acl) {
		d.AddError("Unknown access configuration", "Access configuration must be known at apply.")
		return nil, d
	}
	var sets map[string]*AccessPolicySetModel
	d.Append(policies.ElementsAs(ctx, &sets, false)...)
	if d.HasError() {
		return nil, d
	}
	total := 0
	for _, id := range keys(policies.Elements()) {
		pp := root.AtName("policy_sets").AtMapKey(id)
		model := sets[id]
		if model == nil {
			d.AddAttributeError(pp, "Missing access policy", "Policy must not be null.")
			continue
		}
		selector, ok := api.RegistryIdentitySelector_value["REGISTRY_IDENTITY_SELECTOR_"+model.IdentitySelector.ValueString()]
		if !ok || selector == 0 {
			d.AddAttributeError(pp.AtName("identity_selector"), "Invalid identity selector", "Select a supported identity class.")
		}
		set := &api.RegistryAccessPolicySet{Id: id, IdentitySelector: api.RegistryIdentitySelector(selector)}
		var rules map[string]*AccessRuleModel
		diags := model.Rules.ElementsAs(ctx, &rules, false)
		d.Append(diags...)
		if diags.HasError() {
			continue
		}
		total += len(rules)
		for _, rid := range keys(model.Rules.Elements()) {
			rp := pp.AtName("rules").AtMapKey(rid)
			rule := rules[rid]
			if rule == nil {
				d.AddAttributeError(rp, "Missing access rule", "Rule must not be null.")
				continue
			}
			if !validExpression(rule.Expression.ValueString()) {
				d.AddAttributeError(rp.AtName("expression"), "Invalid CEL expression", "Expression must not be blank.")
			}
			set.Rules = append(set.Rules, &api.RegistryAccessRule{Id: rid, Expression: rule.Expression.ValueString()})
		}
		out.PolicySets = append(out.PolicySets, set)
	}
	if total > 128 {
		d.AddAttributeError(root.AtName("policy_sets"), "Too many access rules", "At most 128 total access rules are allowed.")
	}
	var diags diag.Diagnostics
	out.RequestIpAcl, diags = aclInput(ctx, acl, root.AtName("request_ip_acl"))
	d.Append(diags...)
	validateProto(out, &d)
	if d.HasError() {
		return nil, d
	}
	return out, d
}

// ToProto converts a lifecycle rule and preserves diagnostics for each independent condition.
func (m *LifecycleRuleModel) ToProto(id string) (*api.RegistryLifecycleRule, diag.Diagnostics) {
	var d diag.Diagnostics
	rp := path.Root("rules").AtMapKey(id)
	if m == nil {
		d.AddAttributeError(rp, "Missing lifecycle rule", "Rule must not be null.")
		return nil, d
	}
	r := &api.RegistryLifecycleRule{RuleId: id, Regex: m.Regex.ValueString()}
	if _, err := regexp.Compile(r.Regex); err != nil {
		d.AddAttributeError(rp.AtName("regex"), "Invalid regex", err.Error())
	}
	if !m.OlderThan.IsNull() {
		var err error
		r.OlderThan, err = parseAge(m.OlderThan.ValueString())
		if err != nil {
			d.AddAttributeError(rp.AtName("older_than"), "Invalid duration", err.Error())
		}
	}
	if !m.KeepNewest.IsNull() {
		n := m.KeepNewest.ValueInt64()
		if n < 0 || n > math.MaxUint32 {
			d.AddAttributeError(rp.AtName("keep_newest"), "Invalid retention count", "Count must fit an unsigned 32-bit integer.")
		} else {
			u := uint32(n)
			r.KeepNewest = &u
		}
	}
	if m.OlderThan.IsNull() && m.KeepNewest.IsNull() {
		d.AddAttributeError(rp, "Missing lifecycle condition", "Rule requires older_than or keep_newest.")
	}
	return r, d
}

// lifecycleInput validates the complete authoritative document before any API write.
func lifecycleInput(ctx context.Context, rules types.Map, enabled types.Bool) (*api.RegistryLifecyclePolicy, diag.Diagnostics) {
	var d diag.Diagnostics
	out := &api.RegistryLifecyclePolicy{Enabled: enabled.ValueBool()}
	if !known(rules) || !known(enabled) {
		d.AddError("Unknown lifecycle policy", "Lifecycle content must be known at apply.")
		return nil, d
	}
	var models map[string]*LifecycleRuleModel
	d.Append(rules.ElementsAs(ctx, &models, false)...)
	if d.HasError() {
		return nil, d
	}
	for _, id := range keys(rules.Elements()) {
		rule, diags := models[id].ToProto(id)
		d.Append(diags...)
		if rule != nil {
			out.Rules = append(out.Rules, rule)
		}
	}
	validateProto(out, &d)
	if d.HasError() {
		return nil, d
	}
	return out, d
}

// timestamp preserves absent timestamps.
func timestamp(t *timestamppb.Timestamp) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.AsTime().UTC().Format(time.RFC3339Nano))
}

// aclInput checks server constraints absent from protobuf annotations and preserves conversion diagnostics.
func aclInput(ctx context.Context, acl types.Object, root path.Path) (*api.RegistryIpAcl, diag.Diagnostics) {
	var d diag.Diagnostics
	if !known(acl) {
		d.AddError("Unknown IP ACL", "ACL must be known at apply.")
		return nil, d
	}
	if acl.IsNull() {
		return nil, d
	}
	var model IPACLModel
	d.Append(acl.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if d.HasError() {
		return nil, d
	}
	out := &api.RegistryIpAcl{}
	d.Append(model.AllowCIDRs.ElementsAs(ctx, &out.AllowCidrs, false)...)
	d.Append(model.DenyCIDRs.ElementsAs(ctx, &out.DenyCidrs, false)...)
	if d.HasError() {
		return nil, d
	}
	for name, cidrs := range map[string][]string{"allow_cidrs": out.AllowCidrs, "deny_cidrs": out.DenyCidrs} {
		for _, cidr := range cidrs {
			if !validCIDR(cidr) {
				d.AddAttributeError(root.AtName(name), "Invalid CIDR", fmt.Sprintf("%q must be a canonical CIDR without host bits", cidr))
			}
		}
		sort.Strings(cidrs)
	}
	n := len(out.AllowCidrs) + len(out.DenyCidrs)
	if n == 0 || n > 128 {
		d.AddAttributeError(root, "Invalid IP ACL", "A present ACL requires 1–128 total networks.")
	}
	return out, d
}

// validateAccessBounds checks counts even when unrelated expression or CIDR values are unknown.
func validateAccessBounds(ctx context.Context, policies types.Map, acl types.Object, d *diag.Diagnostics) {
	total := 0
	for _, value := range policies.Elements() {
		if value.IsUnknown() || value.IsNull() {
			continue
		}
		var model AccessPolicySetModel
		d.Append(value.(types.Object).As(ctx, &model, basetypes.ObjectAsOptions{})...)
		total += len(model.Rules.Elements())
	}
	if total > 128 {
		d.AddError("Too many access rules", "At most 128 total access rules are allowed.")
	}
	if acl.IsNull() || acl.IsUnknown() {
		return
	}
	var model IPACLModel
	d.Append(acl.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if d.HasError() {
		return
	}
	count := len(model.AllowCIDRs.Elements()) + len(model.DenyCIDRs.Elements())
	if count > 128 || (count == 0 && !model.AllowCIDRs.IsUnknown() && !model.DenyCIDRs.IsUnknown()) {
		d.AddError("Invalid IP ACL", "A present ACL requires 1–128 total networks.")
	}
}

// validateLifecycleConditions checks condition presence independently of unknown expressions.
func validateLifecycleConditions(ctx context.Context, rules types.Map, d *diag.Diagnostics) {
	for id, value := range rules.Elements() {
		if value.IsUnknown() || value.IsNull() {
			continue
		}
		var model LifecycleRuleModel
		d.Append(value.(types.Object).As(ctx, &model, basetypes.ObjectAsOptions{})...)
		if model.OlderThan.IsNull() && model.KeepNewest.IsNull() {
			d.AddError("Missing lifecycle condition", fmt.Sprintf("Rule %s requires older_than or keep_newest.", id))
		}
	}
}

// ActorModel describes a server-reported request identity.
type ActorModel struct {
	UserUID  types.String `tfsdk:"user_uid"`
	Username types.String `tfsdk:"username"`
}

// AccessModeModel describes effective namespace access and its reasons.
type AccessModeModel struct {
	Mode       types.String `tfsdk:"mode"`
	Reasons    types.Set    `tfsdk:"reasons"`
	UpdateTime types.String `tfsdk:"update_time"`
}

// ContentStatusModel represents exact unsigned counters as Terraform numbers.
type ContentStatusModel struct {
	UsageBytes      types.Number `tfsdk:"usage_bytes"`
	BlobCount       types.Number `tfsdk:"blob_count"`
	ManifestCount   types.Number `tfsdk:"manifest_count"`
	RepositoryCount types.Number `tfsdk:"repository_count"`
	TagCount        types.Number `tfsdk:"tag_count"`
	UpdateTime      types.String `tfsdk:"update_time"`
}

// actorValue preserves absent actor observations and propagates conversion diagnostics.
func actorValue(ctx context.Context, actor *api.RegistryRequestActor) (types.Object, diag.Diagnostics) {
	var model *ActorModel
	if actor != nil {
		model = &ActorModel{UserUID: types.StringValue(actor.UserUid), Username: types.StringValue(actor.Username)}
	}
	return types.ObjectValueFrom(ctx, typesOf(actorAttributes()), model)
}

// namespaceObjects converts shared observations through concrete nested models.
func namespaceObjects(ctx context.Context, n *api.RegistryNamespace) (created, updated, mode, content types.Object, diagnostics diag.Diagnostics) {
	var d, diags diag.Diagnostics
	created, diags = actorValue(ctx, n.CreatedBy)
	d.Append(diags...)
	updated, diags = actorValue(ctx, n.UpdatedBy)
	d.Append(diags...)
	ts := namespaceTypes()
	var access *AccessModeModel
	if n.AccessMode != nil {
		reasons := make([]string, len(n.AccessMode.Reasons))
		for i, reason := range n.AccessMode.Reasons {
			reasons[i] = reason.String()
		}
		value, diags := types.SetValueFrom(ctx, types.StringType, reasons)
		d.Append(diags...)
		access = &AccessModeModel{Mode: types.StringValue(n.AccessMode.Mode.String()), Reasons: value, UpdateTime: timestamp(n.AccessMode.UpdateTime)}
	}
	mode, diags = types.ObjectValueFrom(ctx, ts["access_mode"].(types.ObjectType).AttrTypes, access)
	d.Append(diags...)
	var status *ContentStatusModel
	if c := n.ContentStatus; c != nil {
		status = &ContentStatusModel{UsageBytes: number(c.UsageBytes), BlobCount: number(c.BlobCount), ManifestCount: number(c.ManifestCount), RepositoryCount: number(c.RepositoryCount), TagCount: number(c.TagCount), UpdateTime: timestamp(c.UpdateTime)}
	}
	content, diags = types.ObjectValueFrom(ctx, ts["content_status"].(types.ObjectType).AttrTypes, status)
	d.Append(diags...)
	return created, updated, mode, content, d
}

// number preserves every uint64 bit in a Terraform numeric observation.
func number(value uint64) types.Number {
	return types.NumberValue(new(big.Float).SetPrec(64).SetUint64(value))
}

// nullEntry defers null nested entries to the schema's required-attribute diagnostics.
func nullEntry(values types.Map) bool {
	for _, value := range values.Elements() {
		if containsNullEntry(value) {
			return true
		}
	}
	return false
}

// containsNullEntry identifies null map entries at every policy nesting level.
func containsNullEntry(value attr.Value) bool {
	if value.IsNull() {
		return true
	}
	if value.IsUnknown() {
		return false
	}
	switch v := value.(type) {
	case types.Map:
		for _, entry := range v.Elements() {
			if containsNullEntry(entry) {
				return true
			}
		}
	case types.Object:
		for name, entry := range v.Attributes() {
			switch name {
			case "identity_selector", "expression", "regex", "rules":
				if entry.IsNull() {
					return true
				}
			}
			if nested, ok := entry.(types.Map); ok && !nested.IsNull() && containsNullEntry(nested) {
				return true
			}
		}
	}
	return false
}
