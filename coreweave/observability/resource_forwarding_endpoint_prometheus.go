package observability

import (
	"context"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.ResourceWithConfigure      = &PrometheusForwardingEndpointResource{}
	_ resource.ResourceWithValidateConfig = &PrometheusForwardingEndpointResource{}
	_ resource.ResourceWithImportState    = &PrometheusForwardingEndpointResource{}
)

func NewForwardingEndpointPrometheusResource() resource.Resource {
	return &PrometheusForwardingEndpointResource{}
}

type PrometheusForwardingEndpointResource struct {
	coretf.CoreResource
}

func (r *PrometheusForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_observability_telemetry_relay_endpoint_prometheus"
}

func (r *PrometheusForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := commonEndpointSchema()

	attributes["endpoint"] = schema.StringAttribute{
		MarkdownDescription: "The Prometheus Remote Write endpoint URL, typically ending in `/api/v1/write`. Plain HTTP is rejected, as are targets resolving to loopback, private, or link-local addresses.",
		Required:            true,
		Validators: []validator.String{
			stringvalidator.LengthBetween(1, endpointURLMaxLength),
			stringvalidator.RegexMatches(httpsURLPattern, "must be an https:// URL"),
		},
	}

	attributes["tls"] = tlsConfigAttribute()

	attributes["credentials"] = prometheusCredentialsAttribute()

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telemetry Relay Prometheus Remote Write forwarding endpoint. Forwards metrics data to a Prometheus-compatible endpoint.",
		Attributes:          attributes,
	}
}

func (r *PrometheusForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation if any values are unknown (e.g., during plan when the URL
	// or slug comes from another resource).
	if data.Slug.IsUnknown() || data.DisplayName.IsUnknown() || data.Endpoint.IsUnknown() {
		return
	}

	createReq, diagnostics := buildPrometheusCreateRequest(data)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(createReq); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (r *PrometheusForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func getPrometheusCredentials(data model.ForwardingEndpointPrometheus) (credentials *typesv1beta1.PrometheusCredentials, diagnostics diag.Diagnostics) {
	if data.Credentials == nil {
		return nil, nil
	}

	credentials, diags := data.Credentials.ToMsg()
	diagnostics.Append(diags...)
	return credentials, diagnostics
}

// buildPrometheusCreateRequest assembles the CreateEndpointRequest from a
// model whose credentials came from the config, so ValidateConfig checks the
// message Create actually sends.
func buildPrometheusCreateRequest(data model.ForwardingEndpointPrometheus) (*clusterv1beta1.CreateEndpointRequest, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	endpointMsg, diags := data.ToMsg()
	diagnostics.Append(diags...)

	creds, diags := getPrometheusCredentials(data)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.GetRef(),
		Spec: endpointMsg.GetSpec(),
	}
	if creds != nil {
		createReq.SetPrometheus(creds)
	}

	return createReq, diagnostics
}

func (r *PrometheusForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read the config first: write-only attributes are stripped from the plan.
	var config model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, diags := buildPrometheusCreateRequest(config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, diags := createEndpoint(ctx, r.Client, createReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save state before polling so a poll failure cannot orphan the endpoint.
	resp.Diagnostics.Append(data.Set(created)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, diags := awaitEndpointReady(ctx, r.Client, createReq.GetRef())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PrometheusForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := readEndpoint(ctx, r.Client, data.Slug.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Deleted out of band: drop it from state so the next plan re-creates it.
	if endpoint == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_Prometheus_case {
		resp.Diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not a Prometheus endpoint")
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading Telemetry Relay Prometheus Forwarding Endpoint", map[string]any{
		"slug":         data.Slug.ValueString(),
		"display_name": data.DisplayName.ValueString(),
		"endpoint":     data.Endpoint.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PrometheusForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Credentials come from the config: write-only attributes are null in the
	// plan, so reading them from there sent empty strings the API rejects.
	var config model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diagnostics := data.ToMsg()
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq := &clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointMsg.GetRef(),
		Spec: endpointMsg.GetSpec(),
	}

	action := credentialAction(
		config.Credentials != nil,
		state.CredentialsConfigured.ValueBool(),
		state.CredentialsVersion,
		config.CredentialsVersion,
	)
	updateReq.SetCredentialAction(action)

	if action == clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE {
		creds, diags := getPrometheusCredentials(config)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		updateReq.SetPrometheus(creds)
	}

	updated, diags := updateEndpoint(ctx, r.Client, updateReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save state before polling so a poll failure doesn't leave state describing
	// the pre-update endpoint.
	resp.Diagnostics.Append(data.Set(updated)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, diags := awaitEndpointReady(ctx, r.Client, updateReq.GetRef())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PrometheusForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data model.ForwardingEndpointPrometheus
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(deleteEndpoint(ctx, r.Client, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.State.RemoveResource(ctx)
}
