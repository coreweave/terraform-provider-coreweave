package cks

import (
	"context"
	"fmt"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	legacyPublicDescription = "Whether the cluster's Kubernetes API server is reachable through CoreWeave's public ingress, " +
		"which supports CoreWeave-managed authentication. This setting is independent of `kube_api_access`."

	kubeAPIAccessDescription = "Access configuration for the cluster's Kubernetes API server, independent of `public`. " +
		"**This feature requires contacting CoreWeave support to enable it for your organization before setting this attribute.** " +
		"When this attribute is omitted, the next apply clears any access configured outside Terraform. " +
		"Changes, including clears, cannot be applied in the same update as a Kubernetes version change."

	publicKubeAPIAccessDescription = "Direct public access to the Kubernetes API server. A present `public` object, including `{}`, enables access; " +
		"omitting it or setting it to `null` disables access. CoreWeave-managed authentication is not currently supported on this endpoint; " +
		"Kubernetes authentication and authorization still apply."

	kubeAPIAccessStatusDescription = "Observed state of Kubernetes API access, known after apply and updated on refresh. " +
		"Values may lag a configuration change until the API observes it. " +
		"They are unknown during any in-place update plan, so providers configured from them receive unknown values during that plan."

	dataSourceKubeAPIAccessDescription = "Access configuration for the cluster's Kubernetes API server, independent of `public`. " +
		"**This feature requires contacting CoreWeave support to enable it for your organization.** " +
		"Null when no access configuration is set."

	dataSourcePublicKubeAPIAccessDescription = "Direct public access configuration. Present when direct public access is enabled; null when it is disabled."

	dataSourceKubeAPIAccessStatusDescription = "Observed state of Kubernetes API access. Values may lag a recent configuration change until the API observes it."

	publicKubeAPIAccessStatusDescription = "Observed state of direct public access. Null when the API reports no public access status."

	kubeAPIAccessEndpointDescription = "The HTTPS URL of the public Kubernetes API endpoint, suitable for a kubeconfig `server`. " +
		"Null until the endpoint is available."

	kubeAPIAccessCADataDescription = "The base64-encoded PEM CA bundle for the Kubernetes API server. " +
		"Use `base64decode` to obtain the PEM text. Null until the API reports a CA."
)

var (
	cidrFormatDescription = fmt.Sprintf("At most %d entries; each must be an IPv4 or IPv6 network address with no host bits set, such as `203.0.113.0/24`.",
		maxKubeAPIAccessCIDRs)

	allowCIDRsSemantics = "Empty allows all sources, subject to `deny_cidrs`. A non-empty set denies every other source, " +
		"so `0.0.0.0/0` alone allows only IPv4 sources and `::/0` alone allows only IPv6 sources."

	allowCIDRsDescription = "Source network prefixes allowed to reach the endpoint. " + allowCIDRsSemantics + " " + cidrFormatDescription

	denyCIDRsDescription = "Source network prefixes denied access to the endpoint. Entries take precedence over `allow_cidrs`. " + cidrFormatDescription

	dataSourceAllowCIDRsDescription = "Source network prefixes allowed to reach the endpoint. " + allowCIDRsSemantics

	dataSourceDenyCIDRsDescription = "Source network prefixes denied access to the endpoint. Entries take precedence over `allow_cidrs`."

	kubeAPIAccessStatusValueDescription = fmt.Sprintf("Acceptance of the endpoint's route and access policy. One of: %s. "+
		"`STATUS_ACCEPTED` does not confirm that the endpoint is reachable or that CIDR rules are enforced.",
		coreweave.EnumMarkdownValues(cksv1beta1.PublicKubeAPIAccessStatus_Status_name, false))
)

func kubeAPIAccessResourceAttribute() schema.SingleNestedAttribute {
	// Protobuf repeated fields cannot distinguish null from empty, so present sets default to empty.
	emptySet := setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{}))

	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: kubeAPIAccessDescription,
		Validators:          []validator.Object{kubeAPIAccessValidator{}},
		Attributes: map[string]schema.Attribute{
			"public": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: publicKubeAPIAccessDescription,
				Attributes: map[string]schema.Attribute{
					"allow_cidrs": schema.SetAttribute{
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: allowCIDRsDescription,
						Default:             emptySet,
					},
					"deny_cidrs": schema.SetAttribute{
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: denyCIDRsDescription,
						Default:             emptySet,
					},
				},
			},
		},
	}
}

// kubeAPIAccessStatusResourceAttribute has no plan modifiers: observations can change on any write.
func kubeAPIAccessStatusResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: kubeAPIAccessStatusDescription,
		Attributes: map[string]schema.Attribute{
			"public": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: publicKubeAPIAccessStatusDescription,
				Attributes: map[string]schema.Attribute{
					"endpoint": schema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessEndpointDescription},
					"status":   schema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessStatusValueDescription},
				},
			},
			"ca_data": schema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessCADataDescription},
		},
	}
}

func kubeAPIAccessDataSourceAttribute() datasourceschema.SingleNestedAttribute {
	return datasourceschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: dataSourceKubeAPIAccessDescription,
		Attributes: map[string]datasourceschema.Attribute{
			"public": datasourceschema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: dataSourcePublicKubeAPIAccessDescription,
				Attributes: map[string]datasourceschema.Attribute{
					"allow_cidrs": datasourceschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: dataSourceAllowCIDRsDescription},
					"deny_cidrs":  datasourceschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: dataSourceDenyCIDRsDescription},
				},
			},
		},
	}
}

func kubeAPIAccessStatusDataSourceAttribute() datasourceschema.SingleNestedAttribute {
	return datasourceschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: dataSourceKubeAPIAccessStatusDescription,
		Attributes: map[string]datasourceschema.Attribute{
			"public": datasourceschema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: publicKubeAPIAccessStatusDescription,
				Attributes: map[string]datasourceschema.Attribute{
					"endpoint": datasourceschema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessEndpointDescription},
					"status":   datasourceschema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessStatusValueDescription},
				},
			},
			"ca_data": datasourceschema.StringAttribute{Computed: true, MarkdownDescription: kubeAPIAccessCADataDescription},
		},
	}
}

var _ validator.Object = kubeAPIAccessValidator{}

// kubeAPIAccessValidator validates known kube_api_access values during configuration validation.
// Unknown values are checked again before apply.
type kubeAPIAccessValidator struct{}

func (kubeAPIAccessValidator) Description(context.Context) string {
	return fmt.Sprintf("allow_cidrs and deny_cidrs each contain at most %d IPv4 or IPv6 network prefixes with no host bits set.", maxKubeAPIAccessCIDRs)
}

func (v kubeAPIAccessValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (kubeAPIAccessValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	resp.Diagnostics.Append(validateKubeAPIAccess(req.ConfigValue, req.Path)...)
}
