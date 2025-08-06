package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"
)

type PolicyDocument struct {
	Version   string      `json:"Version"` // "2008-10-17" or "2012-10-17"
	ID        string      `json:"Id,omitempty"`
	Statement []Statement `json:"Statement"`
}

// Statement is one element of the Statement array.
type Statement struct {
	Sid       string     `json:"Sid,omitempty"`
	Principal Principal  `json:"Principal,omitempty"`
	Effect    string     `json:"Effect"` // "Allow" or "Deny"
	Action    StringList `json:"Action,omitempty"`
	Resource  StringList `json:"Resource,omitempty"`
	Condition Condition  `json:"Condition,omitempty"`
}

// Principal handles either "*" or a map[string][]string.
type Principal struct {
	Any        bool
	Statements map[string][]string
}

func (p *Principal) UnmarshalJSON(b []byte) error {
	// Trim space and check for a bare string
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		p.Any = (s == "*")
		p.Statements = nil
		return nil
	}

	// if not, check for a simple map of strings
	var ms map[string]string
	if err := json.Unmarshal(b, &ms); err == nil {
		p.Any = false
		tmp := make(map[string][]string, len(ms))
		for key, value := range ms {
			tmp[key] = []string{value}
		}
		p.Statements = tmp
		return nil
	}

	// if not, expect an object
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err == nil {
		p.Any = false
		p.Statements = m
		return nil
	}

	// lastly, expect the Principal object
	var pr Principal
	if err := json.Unmarshal(b, &pr); err != nil {
		return fmt.Errorf("principal: expected string, map[string]string, or map[string][]string, got %s: %w", string(b), err)
	}
	p.Any = pr.Any
	p.Statements = pr.Statements
	return nil
}

// MarshalJSON implements the inverse of your UnmarshalJSON.
func (p Principal) MarshalJSON() ([]byte, error) {
	// 1) wildcard principal
	if p.Any {
		return json.Marshal("*")
	}
	// 2) map-based principal
	if p.Statements != nil {
		return json.Marshal(p.Statements)
	}
	// 3) explicit null
	return []byte("null"), nil
}

// StringList handles either a single string or a []string.
type StringList []string

func (sl *StringList) UnmarshalJSON(b []byte) error {
	// Try single string
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*sl = []string{s}
		return nil
	}
	// Try slice of strings
	var ss []string
	if err := json.Unmarshal(b, &ss); err != nil {
		return fmt.Errorf("StringList: expected string or []string, got %s: %w", string(b), err)
	}
	*sl = ss
	return nil
}

// MarshalJSON makes a single-element list emit as a string.
func (sl StringList) MarshalJSON() ([]byte, error) {
	switch len(sl) {
	case 0:
		return []byte("[]"), nil
	case 1:
		return json.Marshal(string(sl[0]))
	default:
		return json.Marshal([]string(sl))
	}
}

// Condition maps operator → (condition key → list of values).
// Single‐string values get normalized into a slice of length 1.
type Condition map[string]map[string][]string

func (c *Condition) UnmarshalJSON(b []byte) error {
	// First unmarshal into a generic map[string]json.RawMessage
	var rawOps map[string]json.RawMessage
	if err := json.Unmarshal(b, &rawOps); err != nil {
		return fmt.Errorf("Condition: expected object, got %s: %w", string(b), err)
	}

	out := make(Condition, len(rawOps))

	for op, rawVals := range rawOps {
		// Unmarshal each operator’s payload into map[string]interface{}
		var kv map[string]interface{}
		if err := json.Unmarshal(rawVals, &kv); err != nil {
			return fmt.Errorf("condition[%s]: expected object, got %s: %w", op, string(rawVals), err)
		}

		// Normalize values into []string
		norm := make(map[string][]string, len(kv))
		for key, v := range kv {
			switch val := v.(type) {
			case string:
				norm[key] = []string{val}
			case []interface{}:
				strs := make([]string, len(val))
				for i, elem := range val {
					s, ok := elem.(string)
					if !ok {
						return fmt.Errorf("condition[%s][%s]: element %v is not a string", op, key, elem)
					}
					strs[i] = s
				}
				norm[key] = strs
			default:
				return fmt.Errorf("condition[%s][%s]: expected string or []string, got %T", op, key, v)
			}
		}

		out[op] = norm
	}

	*c = out
	return nil
}

// MarshalJSON is just the default but ensures your Condition alias is used.
func (c Condition) MarshalJSON() ([]byte, error) {
	// Condition is map[string]map[string][]string, which the default
	// marshaller already handles correctly, so a plain call works:
	return json.Marshal(map[string]map[string][]string(c))
}

// Ensure interface compliance
var (
	_ datasource.DataSource = &BucketPolicyDocumentDataSource{}
)

func NewBucketPolicyDocumentDataSource() datasource.DataSource {
	return &BucketPolicyDocumentDataSource{}
}

type BucketPolicyDocumentDataSource struct{}

type BucketPolicyDocumentModel struct {
	Version   types.String     `tfsdk:"version"`
	ID        types.String     `tfsdk:"id"`
	Statement []StatementModel `tfsdk:"statement"`
	JSON      types.String     `tfsdk:"json"`
}

type StatementModel struct {
	Sid       types.String `tfsdk:"sid"`
	Effect    types.String `tfsdk:"effect"`
	Principal types.Map    `tfsdk:"principal"`
	Action    types.List   `tfsdk:"action"`
	Resource  types.List   `tfsdk:"resource"`
	Condition types.Map    `tfsdk:"condition"`
}

func (d *BucketPolicyDocumentDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_policy_document"
}

func (d *BucketPolicyDocumentDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"version": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The policy version, e.g. `\"2012-10-17\"`",
			},
			"id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "An optional policy identifier",
			},
			"json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The rendered policy document as JSON",
			},
		},
		Blocks: map[string]schema.Block{
			"statement": schema.ListNestedBlock{
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"sid": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "An optional statement identifier",
						},
						"effect": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "`Allow` or `Deny`",
						},
						"action": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "List of action strings, e.g. `[\"s3:PutObject\"]`",
						},
						"resource": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "List of resource ARNs, e.g. `[\"arn:aws:s3:::bucket/*\"]`",
						},
						"principal": schema.MapAttribute{
							ElementType: types.ListType{
								ElemType: types.StringType,
							},
							Optional:            true,
							MarkdownDescription: "Map of principal types to ARNs",
						},
						"condition": schema.MapAttribute{
							ElementType: types.MapType{
								ElemType: types.StringType,
							},
							Optional:            true,
							MarkdownDescription: "Map of condition operators to JSON expressions",
						},
					},
				},
			},
		},
	}
}

func BuildPolicyDocument(ctx context.Context, bm BucketPolicyDocumentModel) PolicyDocument {
	// Build the reusable PolicyDocument
	pd := PolicyDocument{
		Version: bm.Version.ValueString(),
		ID:      bm.ID.ValueString(),
	}

	// Convert each statementModel
	for _, sm := range bm.Statement {
		// principal
		pr := map[string][]string{}
		if !sm.Principal.IsNull() {
			var tmp map[string][]string
			sm.Principal.ElementsAs(ctx, &tmp, false)
			pr = tmp
		}
		// condition
		cond := Condition{}
		if !sm.Condition.IsNull() {
			var tmp Condition
			sm.Condition.ElementsAs(ctx, &tmp, false)
			for op, raw := range tmp {
				cond[op] = raw
			}
		}
		// actions
		acts := make([]string, 0)
		if !sm.Action.IsNull() {
			var tmp []string
			sm.Action.ElementsAs(ctx, &tmp, false)
			acts = tmp
		}
		// resources
		res := make([]string, 0)
		if !sm.Resource.IsNull() {
			var tmp []string
			sm.Resource.ElementsAs(ctx, &tmp, false)
			res = tmp
		}

		pd.Statement = append(pd.Statement, Statement{
			Sid:    sm.Sid.ValueString(),
			Effect: sm.Effect.ValueString(),
			Principal: Principal{
				Statements: pr,
			},
			Action:    acts,
			Resource:  res,
			Condition: cond,
		})
	}

	return pd
}

func (d *BucketPolicyDocumentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var bm BucketPolicyDocumentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &bm)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Translate the Terraform data model to the JSON schema represented by PolicyDocument
	pd := BuildPolicyDocument(ctx, bm)
	// Marshal the document
	bytes, err := json.Marshal(pd)
	if err != nil {
		resp.Diagnostics.AddError("Error marshaling policy JSON", err.Error())
		return
	}

	// Write back into Terraform state
	bm.JSON = types.StringValue(string(bytes))
	resp.Diagnostics.Append(resp.State.Set(ctx, &bm)...)
}

// MustRenderBucketPolicyDocument renders HCL for a bucket policy document
func MustRenderBucketPolicyDocument(_ context.Context, name string, cfg *BucketPolicyDocumentModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	// data block
	block := body.AppendNewBlock(
		"data",
		[]string{"coreweave_object_storage_bucket_policy_document", name},
	)
	b := block.Body()

	// optional id & version
	if !cfg.ID.IsNull() {
		b.SetAttributeValue("id", cty.StringVal(cfg.ID.ValueString()))
	}
	if !cfg.Version.IsNull() {
		b.SetAttributeValue("version", cty.StringVal(cfg.Version.ValueString()))
	}

	// each statement block
	for _, s := range cfg.Statement {
		sb := b.AppendNewBlock("statement", nil).Body()

		if !s.Sid.IsNull() {
			sb.SetAttributeValue("sid", cty.StringVal(s.Sid.ValueString()))
		}
		if !s.Effect.IsNull() {
			sb.SetAttributeValue("effect", cty.StringVal(s.Effect.ValueString()))
		}

		// action list
		if !s.Action.IsNull() {
			var vals []cty.Value
			for _, v := range s.Action.Elements() {
				str := v.(types.String).ValueString()
				vals = append(vals, cty.StringVal(str))
			}
			sb.SetAttributeValue("action", cty.ListVal(vals))
		}

		// resource list
		if !s.Resource.IsNull() {
			var vals []cty.Value
			for _, v := range s.Resource.Elements() {
				str := v.(types.String).ValueString()
				vals = append(vals, cty.StringVal(str))
			}
			sb.SetAttributeValue("resource", cty.ListVal(vals))
		}

		// principal map[string][]string
		if !s.Principal.IsNull() {
			m := map[string]cty.Value{}
			for key, val := range s.Principal.Elements() {
				// val is types.List
				list := val.(types.List)
				var elems []cty.Value
				for _, ev := range list.Elements() {
					elems = append(elems, cty.StringVal(ev.(types.String).ValueString()))
				}
				m[key] = cty.ListVal(elems)
			}
			sb.SetAttributeValue("principal", cty.MapVal(m))
		}

		// condition map[string]map[string][]string
		if !s.Condition.IsNull() {
			outer := map[string]cty.Value{}
			for op, raw := range s.Condition.Elements() {
				// raw is types.Map of string→string
				innerMap := raw.(types.Map).Elements()
				inner := map[string]cty.Value{}
				for k2, v2 := range innerMap {
					inner[k2] = cty.StringVal(v2.(types.String).ValueString())
				}
				outer[op] = cty.MapVal(inner)
			}
			sb.SetAttributeValue("condition", cty.MapVal(outer))
		}
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}
